/**
 * Bounded URL fetcher with SSRF protection, following the repo's Go
 * source-fetcher semantics (ADR-0032): http(s) only, no userinfo/fragment,
 * every DNS result (including every redirect hop) must be a public address,
 * bounded redirects/bytes/timeout, gzip-only compression, HTML/PDF types.
 */

import http from 'node:http';
import https from 'node:https';
import { promises as dns } from 'node:dns';
import type { LookupAddress, LookupOptions } from 'node:dns';
import { isIPv4, isIPv6 } from 'node:net';
import zlib from 'node:zlib';
import type { Readable } from 'node:stream';

import { isIPInNonPublicRange, isSyntheticProxyAddress } from './ip.js';
import { ReaderError } from './errors.js';
import type { Config } from './config.js';

export interface FetchResult {
  mediaType: 'html';
  finalUrl: string;
  status: number;
  contentType: string;
  charset: string;
  body: string;
  bytes: number;
  redirects: number;
}

export interface PDFResult {
  mediaType: 'pdf';
  finalUrl: string;
  status: number;
  contentType: string;
  charset: '';
  body: Buffer;
  bytes: number;
  redirects: number;
}

export type FetchResourceResult = FetchResult | PDFResult;

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
  const resource = await fetchResourceInternal(rawUrl, config, false);
  if (resource.mediaType !== 'html') {
    throw new ReaderError('unsupported_type', `unsupported content type: ${resource.contentType}`);
  }
  return resource;
}

export async function fetchResource(rawUrl: string, config: Config): Promise<FetchResourceResult> {
  return fetchResourceInternal(rawUrl, config, true);
}

async function fetchResourceInternal(rawUrl: string, config: Config, allowPDF: boolean): Promise<FetchResourceResult> {
  let current = validateUrl(rawUrl);
  let redirects = 0;

  for (;;) {
    const hop = await requestOnce(current, config, allowPDF);

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
    if (!HTML_CONTENT_TYPES.has(mediaType) && (mediaType !== 'application/pdf' || !allowPDF)) {
      hop.clearTimeout();
      hop.stream.destroy();
      throw new ReaderError('unsupported_type', `unsupported content type: ${contentType || '(none)'}`);
    }

    const bodyTimer = setTimeout(() => {
      hop.abort(new TimeoutSignal(`upstream body timed out after ${config.fetchTimeoutMs}ms`));
    }, config.fetchTimeoutMs);

    let buffer: Buffer;
    try {
      const byteLimit = mediaType === 'application/pdf' ? config.maxPdfResponseBytes : config.maxResponseBytes;
      buffer = await readBody(hop.stream, hop.headers, byteLimit);
    } catch (err) {
      throw classifyError(err);
    } finally {
      hop.clearTimeout();
      clearTimeout(bodyTimer);
    }

    const hasPdfSignature = buffer.subarray(0, 5).equals(Buffer.from('%PDF-', 'ascii'));
    if (mediaType === 'application/pdf') {
      if (!hasPdfSignature) {
        throw new ReaderError('document_type_mismatch', 'PDF content type does not match the required %PDF- signature');
      }
      return {
        mediaType: 'pdf',
        finalUrl: current.toString(),
        status: hop.status,
        contentType,
        charset: '',
        body: buffer,
        bytes: buffer.byteLength,
        redirects,
      };
    }
    if (hasPdfSignature) {
      throw new ReaderError('document_type_mismatch', 'PDF signature does not match the upstream content type');
    }

    const charset = resolveCharset(contentType, buffer);
    return {
      mediaType: 'html',
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

function requestOnce(url: URL, config: Config, allowPDF: boolean): Promise<Hop> {
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
      lookup: (hostname: string, options: LookupOptions, callback: LookupCallback) => {
        validatingLookup(hostname, options, config, callback);
      },
      headers: {
        'user-agent': config.userAgent,
        accept: allowPDF ? 'text/html,application/xhtml+xml,application/pdf' : 'text/html,application/xhtml+xml',
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

type LookupCallback = (
  err: Error | null,
  address: string | LookupAddress[],
  family?: number,
) => void;

function validatingLookup(
  hostname: string,
  options: LookupOptions,
  config: Config,
  callback: LookupCallback,
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
          if (isIPInNonPublicRange(addr.address) &&
              !(config.allowSyntheticProxyTargets && isSyntheticProxyAddress(addr.address))) {
            callback(
              new UnsafeDestinationSignal(`destination ${hostname} resolves to non-public address ${addr.address}`),
              '',
              0,
            );
            return;
          }
        }
      }
      // net's Happy Eyeballs (autoSelectFamily, on by default) calls the
      // lookup with { all: true } and expects the callback to receive the
      // full address list, per the dns.lookup contract. Answering with the
      // single-address signature makes net iterate the address string
      // character by character and fail with "Invalid IP address".
      if (options.all) {
        callback(null, addresses);
        return;
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

function readBody(stream: http.IncomingMessage, headers: http.IncomingHttpHeaders, byteLimit: number): Promise<Buffer> {
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
      if (total > byteLimit) {
        fail(new ReaderError('response_too_large', `upstream body exceeds ${byteLimit} bytes`));
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
