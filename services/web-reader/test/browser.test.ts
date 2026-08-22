/**
 * Browser engine integration tests. Gated on a real Chromium executable
 * being reachable (explicit NANO_WEB_READER_BROWSER_EXECUTABLE, a binary on
 * the candidate list, or the sandbox-installed chrome-headless-shell); the
 * suite skips cleanly otherwise.
 */

import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import fs from 'node:fs';

import { renderPage, checkRequestUrl, resolveBrowserExecutable, closeBrowser } from '../src/browser.js';
import { loadConfig } from '../src/config.js';

const SANDBOX_SHELL = '/tmp/chrome/chrome-headless-shell-linux64/chrome-headless-shell';

function findExecutable(): string | null {
  try {
    return resolveBrowserExecutable(loadConfig({}));
  } catch {
    if (fs.existsSync(SANDBOX_SHELL)) {
      return SANDBOX_SHELL;
    }
    return null;
  }
}

const executable = findExecutable();

function browserConfig(overrides: Record<string, string> = {}) {
  return loadConfig({
    NANO_WEB_READER_BROWSER_EXECUTABLE: executable ?? '',
    NANO_WEB_READER_ALLOW_PRIVATE_TARGETS: 'true',
    NANO_WEB_READER_BROWSER_TIMEOUT_MS: '20000',
    ...overrides,
  });
}

let base = '';
const server = http.createServer((req, res) => {
  switch (req.url) {
    case '/js-page':
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      res.end(`<!DOCTYPE html>
<html lang="en">
<head><title>JS Rendered Page</title></head>
<body>
  <nav><a href="/">Home</a></nav>
  <main><article id="app"><p>loading</p></article></main>
  <script>
    document.getElementById('app').innerHTML =
      '<h1>Client Side Title</h1>' +
      Array.from({ length: 30 }, (_, i) => '<p>Rendered paragraph ' + i + ' with some real content about vector databases.</p>').join('');
  </script>
</body>
</html>`);
      return;
    case '/redirect-js':
      res.writeHead(301, { location: '/js-page' });
      res.end();
      return;
    case '/json':
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end('{}');
      return;
    default:
      res.writeHead(404, { 'content-type': 'text/html' });
      res.end('<html><body>missing</body></html>');
  }
});

before(async () => {
  if (executable === null) return;
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
});

after(async () => {
  if (executable === null) return;
  await closeBrowser();
  server.closeAllConnections();
  await new Promise<void>((resolve) => server.close(() => resolve()));
});

test('browser engine renders js-driven content', { skip: executable === null }, async () => {
  const rendered = await renderPage(`${base}/js-page`, browserConfig());

  assert.equal(rendered.status, 200);
  assert.equal(rendered.redirects, 0);
  // The static HTML only contains "loading"; a real render must have run
  // the inline script and replaced the DOM.
  assert.ok(rendered.html.includes('Client Side Title'));
  assert.ok(!rendered.html.includes('>loading<'));
});

test('browser engine follows redirects and reports the final url', { skip: executable === null }, async () => {
  const rendered = await renderPage(`${base}/redirect-js`, browserConfig());

  assert.equal(rendered.redirects, 1);
  assert.ok(rendered.finalUrl.endsWith('/js-page'));
  assert.ok(rendered.html.includes('Client Side Title'));
});

test('browser engine rejects non-html content types', { skip: executable === null }, async () => {
  await assert.rejects(
    renderPage(`${base}/json`, browserConfig()),
    (err: unknown) => err instanceof Error && (err as { code?: string }).code === 'unsupported_type',
  );
});

test('browser engine blocks private destinations when not allowed', { skip: executable === null }, async () => {
  const config = browserConfig({ NANO_WEB_READER_ALLOW_PRIVATE_TARGETS: 'false' });

  await assert.rejects(
    renderPage(`${base}/js-page`, config),
    (err: unknown) => err instanceof Error && (err as { code?: string }).code === 'unsafe_destination',
  );
});

test('ssrf request guard classifies urls', async () => {
  const config = browserConfig({ NANO_WEB_READER_ALLOW_PRIVATE_TARGETS: 'false' });
  const verdicts = new Map<string, boolean>();

  assert.equal(await checkRequestUrl('javascript:alert(1)', config), false);
  assert.equal(await checkRequestUrl('ftp://example.com/x', config), false);
  assert.equal(await checkRequestUrl('http://user:pass@example.com/', config), false);
  assert.equal(await checkRequestUrl('http://127.0.0.1/x', config, verdicts), false);
  assert.equal(await checkRequestUrl('http://[::1]/x', config, verdicts), false);
  // Verdict cache short-circuits repeat hostnames.
  assert.equal(await checkRequestUrl('http://127.0.0.1:8080/other', config, verdicts), false);

  const permissive = browserConfig({ NANO_WEB_READER_ALLOW_PRIVATE_TARGETS: 'true' });
  assert.equal(await checkRequestUrl('http://127.0.0.1/x', permissive), true);
});
