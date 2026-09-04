import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import type { AddressInfo } from 'node:net';

import { createReaderServer } from '../src/server.js';
import { loadConfig } from '../src/config.js';
import type { Config } from '../src/config.js';
import type { RenderedPage } from '../src/browser.js';

const ARTICLE_HTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <title>Understanding Vector Databases</title>
  <meta name="description" content="A practical introduction.">
</head>
<body>
  <nav><a href="/">Home</a> <a href="/about">About</a></nav>
  <main>
    <article>
      <h1>Understanding Vector Databases</h1>
      <p>Vector databases store high-dimensional embeddings and support fast
         nearest-neighbor search for retrieval augmented generation systems.</p>
      <p>See the <a href="/docs/intro">introduction guide</a>.</p>
    </article>
  </main>
  <footer>© Example Corp 2026</footer>
</body>
</html>`;

function testConfig(overrides: Partial<Config> = {}): Config {
  return {
    addr: '127.0.0.1:0',
    serviceToken: 'test-token',
    maxConcurrent: 2,
    fetchTimeoutMs: 3_000,
    maxRedirects: 5,
    maxResponseBytes: 1024 * 1024,
    maxPdfResponseBytes: 20 * 1024 * 1024,
    maxContentChars: 50_000,
    maxRequestBodyBytes: 16 * 1024,
    allowPrivateTargets: true,
    allowSyntheticProxyTargets: false,
    userAgent: 'test-agent',
    engine: 'lightweight',
    browserExecutable: '',
    browserTimeoutMs: 5_000,
    browserMaxConcurrent: 2,
    autoUpgradeMinWords: 100,
    ...overrides,
  };
}

let upstreamBase = '';
const PDF = Buffer.from('%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n', 'ascii');
const upstream = http.createServer((req, res) => {
  if (req.url === '/slow') {
    setTimeout(() => {
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      res.end(ARTICLE_HTML);
    }, 300);
    return;
  }
  if (req.url === '/paper') {
    res.writeHead(200, { 'content-type': 'application/pdf' });
    res.end(PDF);
    return;
  }
  res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
  res.end(ARTICLE_HTML);
});

let readerBase = '';
const readerServer = createReaderServer(testConfig());

before(async () => {
  await new Promise<void>((resolve) => upstream.listen(0, '127.0.0.1', resolve));
  const upstreamPort = (upstream.address() as AddressInfo).port;
  upstreamBase = `http://127.0.0.1:${upstreamPort}`;

  await new Promise<void>((resolve) => readerServer.listen(0, '127.0.0.1', resolve));
  const readerPort = (readerServer.address() as AddressInfo).port;
  readerBase = `http://127.0.0.1:${readerPort}`;
});

after(async () => {
  await new Promise<void>((resolve) => readerServer.close(() => resolve()));
  readerServer.closeIdleConnections();
  readerServer.closeAllConnections();
  await new Promise<void>((resolve) => upstream.close(() => resolve()));
  upstream.closeAllConnections();
});

function parse(url: string, body: unknown, token = 'test-token'): Promise<Response> {
  return fetch(`${readerBase}${url}`, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      ...(token === '' ? {} : { authorization: `Bearer ${token}` }),
    },
    body: JSON.stringify(body),
  });
}

interface MultipartPart {
  headers: Record<string, string>;
  body: Buffer;
}

async function readMultipart(res: Response): Promise<MultipartPart[]> {
  const contentType = res.headers.get('content-type') ?? '';
  const match = /boundary="?([^";]+)"?/i.exec(contentType);
  assert.ok(match?.[1], `missing multipart boundary in ${contentType}`);
  const boundary = Buffer.from(`--${match[1]}`, 'ascii');
  const raw = Buffer.from(await res.arrayBuffer());
  const parts: MultipartPart[] = [];
  let cursor = boundary.length + 2;
  while (cursor < raw.length && !raw.subarray(cursor - 2, cursor).equals(Buffer.from('--'))) {
    const headerEnd = raw.indexOf(Buffer.from('\r\n\r\n'), cursor);
    assert.notEqual(headerEnd, -1, 'multipart part has no header terminator');
    const headers: Record<string, string> = {};
    for (const line of raw.subarray(cursor, headerEnd).toString('utf8').split('\r\n')) {
      const split = line.indexOf(':');
      assert.ok(split > 0, `invalid multipart header: ${line}`);
      headers[line.slice(0, split).toLowerCase()] = line.slice(split + 1).trim();
    }
    const next = raw.indexOf(Buffer.concat([Buffer.from('\r\n'), boundary]), headerEnd + 4);
    assert.notEqual(next, -1, 'multipart part has no following boundary');
    parts.push({ headers, body: raw.subarray(headerEnd + 4, next) });
    cursor = next + 2 + boundary.length;
    if (raw.subarray(cursor, cursor + 2).equals(Buffer.from('--'))) break;
    assert.ok(raw.subarray(cursor, cursor + 2).equals(Buffer.from('\r\n')));
    cursor += 2;
  }
  return parts;
}

test('health/live is public', async () => {
  const res = await fetch(`${readerBase}/health/live`);
  assert.equal(res.status, 200);
  const payload = (await res.json()) as { status: string; service: string };
  assert.equal(payload.status, 'live');
  assert.equal(payload.service, 'web-reader');
});

test('parse requires a bearer token when configured', async () => {
  const noAuth = await fetch(`${readerBase}/v1/parse`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ url: `${upstreamBase}/article` }),
  });
  assert.equal(noAuth.status, 401);
  assert.equal(((await noAuth.json()) as { error: { code: string } }).error.code, 'unauthorized');

  const wrongToken = await parse('/v1/parse', { url: `${upstreamBase}/article` }, 'wrong-token');
  assert.equal(wrongToken.status, 401);
  assert.equal(((await wrongToken.json()) as { error: { code: string } }).error.code, 'unauthorized');
});

test('parse returns clean markdown for an article', async () => {
  const res = await parse('/v1/parse', { url: `${upstreamBase}/article` });
  assert.equal(res.status, 200);

  const payload = (await res.json()) as {
    schema_version: string;
    url: string;
    final_url: string;
    title: string;
    description: string;
    site_name: string;
    lang: string;
    extraction: string;
    engine: string;
    upgraded: boolean;
    format: string;
    content: string;
    truncated: boolean;
    word_count: number;
    fetch: { status: number; content_type: string; redirects: number };
  };

  assert.equal(payload.schema_version, '1');
  assert.equal(payload.url, `${upstreamBase}/article`);
  assert.equal(payload.final_url, `${upstreamBase}/article`);
  assert.equal(payload.engine, 'lightweight');
  assert.equal(payload.upgraded, false);
  assert.equal(payload.title, 'Understanding Vector Databases');
  assert.equal(payload.description, 'A practical introduction.');
  assert.equal(payload.lang, 'en');
  assert.equal(payload.extraction, 'readability');
  assert.equal(payload.format, 'markdown');
  assert.equal(payload.truncated, false);
  assert.equal(payload.fetch.status, 200);
  assert.ok(payload.word_count > 5);

  assert.match(payload.content, /nearest-neighbor search/);
  assert.match(payload.content, /\[introduction guide\]\(http:\/\/127\.0\.0\.1:\d+\/docs\/intro\)/);
  // The h1 duplicates the page title and is dropped by Readability,
  // matching jina-ai/reader behavior.
  assert.ok(!payload.content.includes('# Understanding Vector Databases'));
  assert.ok(!payload.content.includes('Example Corp'));
  assert.ok(!payload.content.includes('Home'));
});

test('acquire returns HTML as a metadata-only multipart response', async () => {
  const res = await parse('/v1/acquire', { url: `${upstreamBase}/article` });
  assert.equal(res.status, 200);
  assert.match(res.headers.get('content-type') ?? '', /^multipart\/mixed;/i);

  const parts = await readMultipart(res);
  assert.equal(parts.length, 1);
  assert.equal(parts[0]?.headers['content-type'], 'application/json; charset=utf-8');
  assert.equal(parts[0]?.headers['content-id'], 'metadata');
  const metadata = JSON.parse(parts[0]?.body.toString('utf8') ?? '{}') as {
    schema_version: string;
    media_type: string;
    content: string;
    title: string;
  };
  assert.equal(metadata.schema_version, '1');
  assert.equal(metadata.media_type, 'text/html');
  assert.equal(metadata.title, 'Understanding Vector Databases');
  assert.match(metadata.content, /nearest-neighbor search/);
});

test('acquire returns strict PDF metadata followed by exact raw bytes', async () => {
  const res = await parse('/v1/acquire', { url: `${upstreamBase}/paper` });
  assert.equal(res.status, 200);
  assert.match(res.headers.get('content-type') ?? '', /^multipart\/mixed;/i);

  const parts = await readMultipart(res);
  assert.equal(parts.length, 2);
  assert.equal(parts[0]?.headers['content-type'], 'application/json; charset=utf-8');
  assert.equal(parts[0]?.headers['content-id'], 'metadata');
  const metadata = JSON.parse(parts[0]?.body.toString('utf8') ?? '{}') as {
    schema_version: string;
    media_type: string;
    final_url: string;
    sha256: string;
    fetch: { bytes: number; content_type: string };
  };
  assert.equal(metadata.schema_version, '1');
  assert.equal(metadata.media_type, 'application/pdf');
  assert.equal(metadata.final_url, `${upstreamBase}/paper`);
  assert.equal(metadata.sha256, '904636248025ad20fb9c6bd8b700179a2a42edb5df3636e926c7e09055ee3f75');
  assert.equal(metadata.fetch.bytes, PDF.byteLength);
  assert.equal(metadata.fetch.content_type, 'application/pdf');
  assert.equal(parts[1]?.headers['content-type'], 'application/pdf');
  assert.equal(parts[1]?.headers['content-id'], 'document');
  assert.equal(parts[1]?.headers['content-length'], String(PDF.byteLength));
  assert.deepEqual(parts[1]?.body, PDF);
});

test('acquire uses the same authorization and method boundary as parse', async () => {
  const unauthenticated = await fetch(`${readerBase}/v1/acquire`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ url: `${upstreamBase}/paper` }),
  });
  assert.equal(unauthenticated.status, 401);

  const wrongMethod = await fetch(`${readerBase}/v1/acquire`, {
    headers: { authorization: 'Bearer test-token' },
  });
  assert.equal(wrongMethod.status, 405);
});

test('parse validates the request body', async () => {
  const missing = await parse('/v1/parse', {});
  assert.equal(missing.status, 400);
  assert.equal(((await missing.json()) as { error: { code: string } }).error.code, 'invalid_request');

  const badFormat = await parse('/v1/parse', { url: `${upstreamBase}/article`, format: 'pdf' });
  assert.equal(badFormat.status, 400);

  const unknownField = await parse('/v1/parse', { url: `${upstreamBase}/article`, nope: 1 });
  assert.equal(unknownField.status, 400);
  assert.match(
    ((await unknownField.json()) as { error: { message: string } }).error.message,
    /unknown request field/,
  );

  const badJson = await fetch(`${readerBase}/v1/parse`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', authorization: 'Bearer test-token' },
    body: '{not json',
  });
  assert.equal(badJson.status, 400);
});

test('parse rejects unsafe destinations', async () => {
  const scheme = await parse('/v1/parse', { url: 'ftp://example.com/x' });
  assert.equal(scheme.status, 422);
  assert.equal(((await scheme.json()) as { error: { code: string } }).error.code, 'unsafe_destination');

  const fragment = await parse('/v1/parse', { url: 'https://example.com/#x' });
  assert.equal(fragment.status, 422);
});

test('parse rejects non-html upstream content', async () => {
  const jsonUpstream = http.createServer((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end('{}');
  });
  await new Promise<void>((resolve) => jsonUpstream.listen(0, '127.0.0.1', resolve));
  const port = (jsonUpstream.address() as AddressInfo).port;

  try {
    const res = await parse('/v1/parse', { url: `http://127.0.0.1:${port}/data` });
    assert.equal(res.status, 415);
    assert.equal(((await res.json()) as { error: { code: string } }).error.code, 'unsupported_type');
  } finally {
    jsonUpstream.closeAllConnections();
    await new Promise<void>((resolve) => jsonUpstream.close(() => resolve()));
  }
});

test('parse supports text format and link removal', async () => {
  const res = await parse('/v1/parse', {
    url: `${upstreamBase}/article`,
    format: 'text',
    with_links: false,
  });
  assert.equal(res.status, 200);

  const payload = (await res.json()) as { format: string; content: string };
  assert.equal(payload.format, 'text');
  assert.ok(payload.content.includes('nearest-neighbor search'));
  assert.ok(!payload.content.includes('['));
});

test('parse truncates oversized content on request', async () => {
  const res = await parse('/v1/parse', { url: `${upstreamBase}/article`, max_chars: 40 });
  assert.equal(res.status, 200);

  const payload = (await res.json()) as { content: string; truncated: boolean };
  assert.equal(payload.truncated, true);
  assert.ok(payload.content.endsWith('[content truncated]'));
});

test('parse endpoints enforce method and path contracts', async () => {
  const getParse = await fetch(`${readerBase}/v1/parse`);
  assert.equal(getParse.status, 405);
  assert.equal(((await getParse.json()) as { error: { code: string } }).error.code, 'method_not_allowed');

  const unknown = await fetch(`${readerBase}/v2/nope`);
  assert.equal(unknown.status, 404);
  assert.equal(((await unknown.json()) as { error: { code: string } }).error.code, 'not_found');
});

test('parse returns service_busy at the concurrency limit', async () => {
  const busyServer = createReaderServer(testConfig({ maxConcurrent: 1 }));
  await new Promise<void>((resolve) => busyServer.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${(busyServer.address() as AddressInfo).port}`;

  try {
    const first = fetch(`${base}/v1/parse`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: 'Bearer test-token' },
      body: JSON.stringify({ url: `${upstreamBase}/slow` }),
    });
    await new Promise((resolve) => setTimeout(resolve, 100));

    const second = await fetch(`${base}/v1/parse`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: 'Bearer test-token' },
      body: JSON.stringify({ url: `${upstreamBase}/slow` }),
    });
    assert.equal(second.status, 503);
    assert.equal(((await second.json()) as { error: { code: string } }).error.code, 'service_busy');

    const firstRes = await first;
    assert.equal(firstRes.status, 200);
  } finally {
    busyServer.closeAllConnections();
    await new Promise<void>((resolve) => busyServer.close(() => resolve()));
  }
});

test('loadConfig validates environment values', () => {
  assert.equal(loadConfig({}).addr, '127.0.0.1:8085');
  assert.equal(loadConfig({}).maxConcurrent, 8);
  assert.equal(loadConfig({}).browserMaxConcurrent, 4);
  assert.equal(loadConfig({ NANO_WEB_READER_SERVICE_TOKEN: 'x' }).serviceToken, 'x');
  assert.equal(loadConfig({ NANO_WEB_READER_ADDR: ':8085' }).addr, ':8085');
  assert.throws(() => loadConfig({ NANO_WEB_READER_ADDR: 'bad' }));
  assert.throws(() => loadConfig({ NANO_WEB_READER_MAX_CONCURRENT: 'nope' }));
  assert.equal(loadConfig({ NANO_WEB_READER_ALLOW_PRIVATE_TARGETS: 'true' }).allowPrivateTargets, true);
  assert.equal(loadConfig({}).allowSyntheticProxyTargets, false);
  assert.equal(loadConfig({ NANO_WEB_READER_ALLOW_SYNTHETIC_PROXY_TARGETS: 'true' }).allowSyntheticProxyTargets, true);
  assert.equal(loadConfig({}).maxPdfResponseBytes, 20 * 1024 * 1024);
  assert.equal(loadConfig({ NANO_WEB_READER_MAX_PDF_RESPONSE_BYTES: '4096' }).maxPdfResponseBytes, 4096);
  assert.throws(() => loadConfig({ NANO_WEB_READER_MAX_PDF_RESPONSE_BYTES: 'unbounded' }));

  assert.equal(loadConfig({}).engine, 'auto');
  assert.equal(loadConfig({ NANO_WEB_READER_ENGINE: 'lightweight' }).engine, 'lightweight');
  assert.equal(loadConfig({ NANO_WEB_READER_ENGINE: 'BROWSER' }).engine, 'browser');
  assert.throws(() => loadConfig({ NANO_WEB_READER_ENGINE: 'turbo' }));
  assert.equal(loadConfig({ NANO_WEB_READER_AUTO_UPGRADE_MIN_WORDS: '42' }).autoUpgradeMinWords, 42);
  assert.throws(() => loadConfig({ NANO_WEB_READER_BROWSER_TIMEOUT_MS: 'soon' }));
});

test('auto mode upgrades js-shell pages through the browser engine', async () => {
  // The upstream serves an empty app shell; the "browser" (faked) returns
  // the hydrated DOM a real Chromium would produce.
  const SHELL_HTML = `<!DOCTYPE html>
<html lang="en">
<head><title>Shell</title></head>
<body>
  <nav><a href="/">Home</a></nav>
  <main><article id="app"></article></main>
  <script>/* hydration happens client-side */</script>
</body>
</html>`;

  const HYDRATED_HTML = `<!DOCTYPE html>
<html lang="en">
<head><title>Shell</title></head>
<body>
  <main><article id="app"><h1>Rendered Article</h1><p>${Array.from({ length: 40 }, (_, i) => `hydrated paragraph ${i} about vector databases`).join('</p><p>')}</p></article></main>
</body>
</html>`;

  const shellUpstream = http.createServer((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    res.end(SHELL_HTML);
  });
  await new Promise<void>((resolve) => shellUpstream.listen(0, '127.0.0.1', resolve));
  const shellPort = (shellUpstream.address() as AddressInfo).port;

  let renderCalls = 0;
  const fakeRender = async (url: string): Promise<RenderedPage> => {
    renderCalls += 1;
    assert.equal(url, `http://127.0.0.1:${shellPort}/app`);
    return {
      finalUrl: url,
      status: 200,
      contentType: 'text/html; charset=utf-8',
      html: HYDRATED_HTML,
      bytes: Buffer.byteLength(HYDRATED_HTML),
      redirects: 0,
    };
  };

  const autoServer = createReaderServer(testConfig({ engine: 'auto' }), { renderPage: fakeRender });
  await new Promise<void>((resolve) => autoServer.listen(0, '127.0.0.1', resolve));
  const autoBase = `http://127.0.0.1:${(autoServer.address() as AddressInfo).port}`;

  try {
    const res = await fetch(`${autoBase}/v1/parse`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: 'Bearer test-token' },
      body: JSON.stringify({ url: `http://127.0.0.1:${shellPort}/app` }),
    });
    assert.equal(res.status, 200);

    const payload = (await res.json()) as {
      engine: string;
      upgraded: boolean;
      title: string;
      content: string;
      word_count: number;
      extraction: string;
    };

    assert.equal(renderCalls, 1);
    assert.equal(payload.engine, 'browser');
    assert.equal(payload.upgraded, true);
    assert.equal(payload.extraction, 'readability');
    assert.match(payload.content, /# Rendered Article/);
    assert.ok(payload.word_count > 100);
  } finally {
    autoServer.closeAllConnections();
    await new Promise<void>((resolve) => autoServer.close(() => resolve()));
    shellUpstream.closeAllConnections();
    await new Promise<void>((resolve) => shellUpstream.close(() => resolve()));
  }
});
