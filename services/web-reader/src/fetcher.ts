/**
 * Bounded URL fetcher with SSRF protection, following the repo's Go
 * source-fetcher semantics (ADR-0032): http(s) only, no userinfo/fragment,
 * every DNS result (including every redirect hop) must be a public address,
 * bounded redirects/bytes/timeout, gzip-only compression, HTML-only types.
 */

import http from 'node:http';
import https from 'node:https';
import { promises as dns } from 'node:dns';
import { isIPv4, isIPv6 } from 'node:net';
import zlib from 'node:zlib';
import type { Readable } from 'node:stream';

import { isIPInNonPublicRange } from './ip.js';
import { ReaderError } from './errors.js';
import type { Config } from './config.js';

export interface FetchResult {
  finalUrl: string;
  status: number;
  contentType: string;
  charset: string;
  body: string;
  bytes: number;
  redirects: number;
}

class UnsafeDestinationSignal extends Error {}
class TimeoutSignal extends Error {}

const HTML_CONTENT_TYPES = new Set(['text/html', 'application/xhtml+xml']);
const REDIRECT_STATUSES = new Set([301, 302, 303, 307, 308]);

export function validateUrl(raw: string): URL {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new ReaderError('invalid_request', `invalid url: ${raw}`);
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new ReaderError('unsafe_destination', `only http/https urls are allowed: ${raw}`);
  }
  if (url.username !== '' || url.password !== '') {
    throw new ReaderError('unsafe_destination', `userinfo in url is not allowed: ${raw}`);
  }
  if (url.hash !== '') {
    throw new ReaderError('unsafe_destination', `fragment in url is not allowed: ${raw}`);
  }
  if (url.hostname === '') {
    throw new ReaderError('unsafe_destination', `url has no hostname: ${raw}`);
  }
  if (url.port !== '') {
    const port = Number(url.port);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      throw new ReaderError('unsafe_destination', `invalid port in url: ${raw}`);
    }
  }
  return url;
}

export async function fetchPage(rawUrl: string, config: Config): Promise<FetchResult> {
  let current = validateUrl(rawUrl);
  let redirects = 0;

  for (;;) {
    const hop = await requestOnce(current, config);

    if (REDIRECT_STATUSES.has(hop.status)) {
      const location = hop.headers['location'];
      hop.clearTimeout();
      hop.stream.resume();
      if (typeof location !== 'string' || location === '') {
        throw new ReaderError('upstream_failed', `redirect without location from ${current}`);
      }
      redirects += 1;
      if (redirects > config.maxRedirects) {
        throw new ReaderError('upstream_failed', `too many redirects (max ${config.maxRedirects})`);
      }
      let next: URL;
      try {
        next = new URL(location, current);
      } catch {
        throw new ReaderError('upstream_failed', `invalid redirect location: ${location}`);
      }
      current = validateUrl(next.toString());
      continue;
    }

    if (hop.status >= 400) {
      hop.clearTimeout();
      hop.stream.destroy();
      throw new ReaderError('upstream_failed', `upstream returned status ${hop.status}`);
    }

    const contentType = hop.headers['content-type'] ?? '';
    const mediaType = contentType.split(';')[0]?.trim().toLowerCase() ?? '';
    if (!HTML_CONTENT_TYPES.has(mediaType)) {
      hop.clearTimeout();
      hop.stream.destroy();
      throw new ReaderError('unsupported_type', `unsupported content type: ${contentType || '(none)'}`);
    }

    const bodyTimer = setTimeout(() => {
      hop.abort(new TimeoutSignal(`upstream body timed out after ${config.fetchTimeoutMs}ms`));
    }, config.fetchTimeoutMs);

    let buffer: Buffer;
    try {
      buffer = await readBody(hop.stream, hop.headers, config);
    } catch (err) {
      throw classifyError(err);
    } finally {
      hop.clearTimeout();
      clearTimeout(bodyTimer);
    }

    const charset = resolveCharset(contentType, buffer);
    return {
      finalUrl: current.toString(),
      status: hop.status,
      contentType,
      charset,
      body: decodeBody(buffer, charset),
      bytes: buffer.byteLength,
      redirects,
    };
  }
}

interface Hop {
  status: number;
  headers: http.IncomingHttpHeaders;
  stream: http.IncomingMessage;
  clearTimeout: () => void;
  abort: (err: Error) => void;
}

function requestOnce(url: URL, config: Config): Promise<Hop> {
  // IP-literal hosts never reach the `lookup` hook (node connects directly),
  // so they must be validated here as well; this covers every redirect hop.
  if (!config.allowPrivateTargets) {
    const hostname = url.hostname.replace(/^\[|\]$/g, '');
    if ((isIPv4(hostname) || isIPv6(hostname)) && isIPInNonPublicRange(hostname)) {
      return Promise.reject(
        new ReaderError('unsafe_destination', `destination ${hostname} is a non-public address`),
      );
    }
  }

  return new Promise((resolve, reject) => {
    const transport = url.protocol === 'https:' ? https : http;

    const req = transport.request(url, {
      agent: false,
      lookup: (hostname: string, _options: unknown, callback: (err: Error | null, address: string, family: number) => void) => {
        validatingLookup(hostname, config, callback);
      },
      headers: {
        'user-agent': config.userAgent,
        accept: 'text/html,application/xhtml+xml',
        'accept-encoding': 'gzip',
      },
    });

    const timer = setTimeout(() => {
      req.destroy(new TimeoutSignal(`upstream timed out after ${config.fetchTimeoutMs}ms`));
    }, config.fetchTimeoutMs);

    req.on('response', (res) => {
      resolve({
        status: res.statusCode ?? 0,
        headers: res.headers,
        stream: res,
        clearTimeout: () => clearTimeout(timer),
        abort: (err: Error) => {
          clearTimeout(timer);
          req.destroy(err);
        },
      });
    });
    req.on('error', (err) => {
      clearTimeout(timer);
      reject(classifyError(err));
    });
    req.end();
  });
}

function validatingLookup(
  hostname: string,
  config: Config,
  callback: (err: Error | null, address: string, family: number) => void,
): void {
  dns
    .lookup(hostname, { all: true, verbatim: true })
    .then((addresses) => {
      if (addresses.length === 0) {
        callback(new Error(`no address found for ${hostname}`), '', 0);
        return;
      }
      if (!config.allowPrivateTargets) {
        for (const addr of addresses) {
          if (isIPInNonPublicRange(addr.address)) {
            callback(
              new UnsafeDestinationSignal(`destination ${hostname} resolves to non-public address ${addr.address}`),
              '',
              0,
            );
            return;
          }
        }
      }
      const first = addresses[0];
      if (!first) {
        callback(new Error(`no address found for ${hostname}`), '', 0);
        return;
      }
      callback(null, first.address, first.family);
    })
    .catch((err: unknown) => {
      callback(err instanceof Error ? err : new Error(String(err)), '', 0);
    });
}

function readBody(stream: http.IncomingMessage, headers: http.IncomingHttpHeaders, config: Config): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const encoding = (headers['content-encoding'] ?? '').trim().toLowerCase();
    if (encoding !== '' && encoding !== 'gzip' && encoding !== 'identity') {
      stream.destroy();
      reject(new ReaderError('upstream_failed', `unsupported content-encoding: ${encoding}`));
      return;
    }

    let source: Readable = stream;
    if (encoding === 'gzip') {
      source = stream.pipe(zlib.createGunzip());
    }

    let settled = false;
    const chunks: Buffer[] = [];
    let total = 0;

    const fail = (err: unknown) => {
      if (settled) return;
      settled = true;
      if (err instanceof ReaderError) {
        reject(err);
      } else {
        stream.destroy();
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    };

    source.on('data', (chunk: Buffer) => {
      total += chunk.byteLength;
      if (total > config.maxResponseBytes) {
        fail(new ReaderError('response_too_large', `upstream body exceeds ${config.maxResponseBytes} bytes`));
        stream.destroy();
        return;
      }
      chunks.push(chunk);
    });
    source.on('end', () => {
      if (settled) return;
      settled = true;
      resolve(Buffer.concat(chunks));
    });
    source.on('error', fail);
    stream.on('error', fail);
  });
}

function classifyError(err: unknown): ReaderError {
  if (err instanceof ReaderError) {
    return err;
  }
  if (err instanceof UnsafeDestinationSignal) {
    return new ReaderError('unsafe_destination', err.message);
  }
  if (err instanceof TimeoutSignal) {
    return new ReaderError('upstream_failed', err.message);
  }
  const code = (err as NodeJS.ErrnoException).code;
  if (code === 'ENOTFOUND' || code === 'EAI_AGAIN') {
    return new ReaderError('upstream_failed', `dns resolution failed: ${errorText(err)}`);
  }
  return new ReaderError('upstream_failed', `upstream request failed: ${errorText(err)}`);
}

function resolveCharset(contentType: string, body: Buffer): string {
  const headerMatch = /charset\s*=\s*"?([\w-]+)"?/i.exec(contentType);
  if (headerMatch?.[1]) {
    return headerMatch[1].toLowerCase();
  }

  // Sniff <meta charset> in the first 2KB, like browsers do.
  const head = body.subarray(0, 2048).toString('latin1');
  const metaCharset =
    /<meta[^>]+charset\s*=\s*["']?([\w-]+)/i.exec(head)?.[1] ??
    /<meta[^>]+content\s*=\s*["'][^"']*charset=([\w-]+)/i.exec(head)?.[1];
  if (metaCharset) {
    return metaCharset.toLowerCase();
  }

  if (body[0] === 0xef && body[1] === 0xbb && body[2] === 0xbf) return 'utf-8';
  if (body[0] === 0xfe && body[1] === 0xff) return 'utf-16be';
  if (body[0] === 0xff && body[1] === 0xfe) return 'utf-16le';

  return 'utf-8';
}

function decodeBody(body: Buffer, charset: string): string {
  for (const label of [charset, 'utf-8']) {
    try {
      return new TextDecoder(label, { fatal: false }).decode(body);
    } catch {
      // Unknown label; fall through to the next candidate.
    }
  }
  return body.toString('utf-8');
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
