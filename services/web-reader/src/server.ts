/**
 * HTTP contract for the web-reader sidecar:
 *
 *   GET  /health/live          -> {"status":"live","service":"web-reader"}
 *   POST /v1/parse             -> parse a page and return clean content
 *
 * The parse endpoint accepts a JSON body:
 *   {"url": "...", "format": "markdown"|"text"|"html",
 *    "with_links": true, "with_images": true, "max_chars": 250000}
 * and always answers with `{"error":{"code","message"}}` on failure, using
 * the stable error codes shared with the repo's Go sidecars.
 *
 * Content is produced by the engine selected with NANO_WEB_READER_ENGINE
 * (lightweight | browser | auto); the response reports which engine won via
 * the `engine` field and whether auto mode upgraded via `upgraded`.
 */

import http from 'node:http';
import { createHash, timingSafeEqual } from 'node:crypto';

import { OUTPUT_FORMATS, type OutputFormat } from './reader.js';
import { createEngine, type Engine, type EngineDeps } from './engine.js';
import { ReaderError, errorBody } from './errors.js';
import type { Config } from './config.js';

const SERVICE_NAME = 'web-reader';
const SCHEMA_VERSION = '1';
const PARSE_PATH = '/v1/parse';
const HEALTH_PATH = '/health/live';

const REQUEST_KEYS = new Set(['url', 'format', 'with_links', 'with_images', 'max_chars']);

interface ParseRequest {
  url: string;
  format: OutputFormat;
  withLinks: boolean;
  withImages: boolean;
  maxChars: number;
}

class ConcurrencyGate {
  private active = 0;

  constructor(private readonly max: number) {}

  tryAcquire(): boolean {
    if (this.active >= this.max) {
      return false;
    }
    this.active += 1;
    return true;
  }

  release(): void {
    this.active = Math.max(0, this.active - 1);
  }
}

export function createReaderServer(config: Config, engineDeps: Partial<EngineDeps> = {}): http.Server {
  const gate = new ConcurrencyGate(config.maxConcurrent);
  const engine = createEngine(config, engineDeps);

  return http.createServer((req, res) => {
    handleRequest(req, res, config, gate, engine).catch((err: unknown) => {
      if (res.writableEnded || res.destroyed) {
        return;
      }
      const readerError =
        err instanceof ReaderError
          ? err
          : new ReaderError('internal_error', 'internal error');
      if (!(err instanceof ReaderError)) {
        // Unexpected failures are logged server-side, never surfaced raw.
        console.error('[web-reader] unexpected error:', err);
      }
      respondJson(res, readerError.status, errorBody(readerError.code, readerError.message));
    });
  });
}

async function handleRequest(
  req: http.IncomingMessage,
  res: http.ServerResponse,
  config: Config,
  gate: ConcurrencyGate,
  engine: Engine,
): Promise<void> {
  const path = splitPath(req.url);

  if (path === HEALTH_PATH) {
    if (req.method !== 'GET' && req.method !== 'HEAD') {
      throw new ReaderError('method_not_allowed', `${req.method} is not allowed on ${HEALTH_PATH}`);
    }
    respondJson(res, 200, JSON.stringify({ status: 'live', service: SERVICE_NAME }));
    return;
  }

  if (path === PARSE_PATH) {
    if (req.method !== 'POST') {
      throw new ReaderError('method_not_allowed', `${req.method} is not allowed on ${PARSE_PATH}`);
    }
    if (!authorized(req, config.serviceToken)) {
      throw new ReaderError('unauthorized', 'missing or invalid bearer token');
    }
    await handleParse(req, res, config, gate, engine);
    return;
  }

  throw new ReaderError('not_found', `unknown path: ${path}`);
}

async function handleParse(
  req: http.IncomingMessage,
  res: http.ServerResponse,
  config: Config,
  gate: ConcurrencyGate,
  engine: Engine,
): Promise<void> {
  const parsed = parseRequestBody(await readJsonBody(req, config.maxRequestBodyBytes), config);

  if (!gate.tryAcquire()) {
    throw new ReaderError('service_busy', `service is at its concurrency limit (${config.maxConcurrent})`);
  }

  try {
    const outcome = await engine.readPage(parsed.url, {
      format: parsed.format,
      withLinks: parsed.withLinks,
      withImages: parsed.withImages,
    });
    const page = outcome.page;

    let content = page.content;
    let truncated = false;
    if (content.length > parsed.maxChars) {
      content = `${content.slice(0, parsed.maxChars)}\n\n[content truncated]`;
      truncated = true;
    }

    const payload = {
      schema_version: SCHEMA_VERSION,
      url: parsed.url,
      final_url: outcome.finalUrl,
      title: page.title,
      description: page.description,
      site_name: page.siteName,
      published_time: page.publishedTime,
      lang: page.lang,
      extraction: page.extraction,
      engine: outcome.engine,
      upgraded: outcome.upgraded,
      format: parsed.format,
      content,
      char_count: content.length,
      word_count: page.wordCount,
      truncated,
      fetch: outcome.fetch,
    };
    respondJson(res, 200, JSON.stringify(payload));
  } finally {
    gate.release();
  }
}

function parseRequestBody(raw: unknown, config: Config): ParseRequest {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    throw new ReaderError('invalid_request', 'request body must be a json object');
  }
  const body = raw as Record<string, unknown>;
  for (const key of Object.keys(body)) {
    if (!REQUEST_KEYS.has(key)) {
      throw new ReaderError('invalid_request', `unknown request field: ${key}`);
    }
  }

  const url = body['url'];
  if (typeof url !== 'string' || url.trim() === '') {
    throw new ReaderError('invalid_request', 'url is required and must be a non-empty string');
  }

  const formatRaw = body['format'] ?? 'markdown';
  if (typeof formatRaw !== 'string' || !OUTPUT_FORMATS.includes(formatRaw as OutputFormat)) {
    throw new ReaderError('invalid_request', `format must be one of ${OUTPUT_FORMATS.join(', ')}`);
  }

  const withLinks = booleanField(body, 'with_links', true);
  const withImages = booleanField(body, 'with_images', true);

  const maxCharsRaw = body['max_chars'] ?? config.maxContentChars;
  if (typeof maxCharsRaw !== 'number' || !Number.isInteger(maxCharsRaw) || maxCharsRaw < 1 || maxCharsRaw > config.maxContentChars) {
    throw new ReaderError('invalid_request', `max_chars must be an integer between 1 and ${config.maxContentChars}`);
  }

  return {
    url: url.trim(),
    format: formatRaw as OutputFormat,
    withLinks,
    withImages,
    maxChars: maxCharsRaw,
  };
}

function booleanField(body: Record<string, unknown>, key: string, fallback: boolean): boolean {
  const value = body[key];
  if (value === undefined) return fallback;
  if (typeof value !== 'boolean') {
    throw new ReaderError('invalid_request', `${key} must be a boolean`);
  }
  return value;
}

function readJsonBody(req: http.IncomingMessage, limit: number): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let total = 0;
    let settled = false;

    const fail = (err: ReaderError) => {
      if (settled) return;
      settled = true;
      // Pause instead of destroying so the 4xx response still reaches the
      // client; Node closes the connection afterwards since the request
      // body was not fully consumed.
      req.pause();
      reject(err);
    };

    req.on('data', (chunk: Buffer) => {
      total += chunk.byteLength;
      if (total > limit) {
        fail(new ReaderError('invalid_request', `request body exceeds ${limit} bytes`));
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => {
      if (settled) return;
      settled = true;
      try {
        const text = Buffer.concat(chunks).toString('utf8');
        resolve(text.trim() === '' ? {} : JSON.parse(text));
      } catch {
        reject(new ReaderError('invalid_request', 'request body is not valid json'));
      }
    });
    req.on('error', (err) => {
      if (settled) return;
      settled = true;
      reject(new ReaderError('invalid_request', `failed to read request body: ${err.message}`));
    });
  });
}

function authorized(req: http.IncomingMessage, token: string): boolean {
  if (token === '') {
    return true;
  }
  const header = req.headers.authorization ?? '';
  const prefix = 'bearer ';
  if (header.length < prefix.length || header.slice(0, prefix.length).toLowerCase() !== prefix) {
    return false;
  }
  const provided = header.slice(prefix.length);
  const providedHash = createHash('sha256').update(provided).digest();
  const expectedHash = createHash('sha256').update(token).digest();
  return timingSafeEqual(providedHash, expectedHash);
}

function splitPath(raw: string | undefined): string {
  if (raw === undefined) return '/';
  const queryIndex = raw.indexOf('?');
  const path = queryIndex === -1 ? raw : raw.slice(0, queryIndex);
  return path.length > 1 && path.endsWith('/') ? path.slice(0, -1) : path;
}

function respondJson(res: http.ServerResponse, status: number, body: string): void {
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'content-length': Buffer.byteLength(body),
  });
  res.end(body);
}
