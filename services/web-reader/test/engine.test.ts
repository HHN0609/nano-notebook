import { test } from 'node:test';
import assert from 'node:assert/strict';

import { createEngine } from '../src/engine.js';
import { ReaderError } from '../src/errors.js';
import type { Config } from '../src/config.js';
import type { FetchResult } from '../src/fetcher.js';
import type { RenderedPage } from '../src/browser.js';

const OPTIONS = { format: 'markdown' as const, withLinks: true, withImages: true };

function testConfig(overrides: Partial<Config> = {}): Config {
  return {
    addr: '127.0.0.1:0',
    serviceToken: 't',
    maxConcurrent: 4,
    fetchTimeoutMs: 3_000,
    maxRedirects: 5,
    maxResponseBytes: 1024 * 1024,
    maxContentChars: 250_000,
    maxRequestBodyBytes: 16 * 1024,
    allowPrivateTargets: true,
    allowSyntheticProxyTargets: false,
    userAgent: 'test',
    engine: 'auto',
    browserExecutable: '',
    browserTimeoutMs: 5_000,
    browserMaxConcurrent: 2,
    autoUpgradeMinWords: 100,
    ...overrides,
  };
}

function articleHtml(words: number, title = 'Article'): string {
  const text = Array.from({ length: words }, (_, i) => `word${i}`).join(' ');
  return `<html><head><title>${title}</title></head><body><article><h1>${title}</h1><p>${text}</p></article></body></html>`;
}

function fakeFetch(body: string, extra: Partial<FetchResult> = {}): () => Promise<FetchResult> {
  return async () => ({
    finalUrl: 'https://example.com/a',
    status: 200,
    contentType: 'text/html; charset=utf-8',
    charset: 'utf-8',
    body,
    bytes: Buffer.byteLength(body),
    redirects: 0,
    ...extra,
  });
}

function fakeRender(html: string, extra: Partial<RenderedPage> = {}): () => Promise<RenderedPage> {
  return async () => ({
    finalUrl: 'https://example.com/a',
    status: 200,
    contentType: 'text/html; charset=utf-8',
    html,
    bytes: Buffer.byteLength(html),
    redirects: 0,
    ...extra,
  });
}

function failing(what: () => never): () => Promise<never> {
  return async () => {
    throw what();
  };
}

function forbiddenRender(): never {
  throw new ReaderError('engine_unavailable', 'no browser executable found');
}

test('lightweight mode never touches the browser engine', async () => {
  const engine = createEngine(testConfig({ engine: 'lightweight' }), {
    fetchPage: fakeFetch(articleHtml(150)),
    renderPage: failing(() => {
      throw new Error('browser engine must not be called');
    }),
  });

  const outcome = await engine.readPage('https://example.com/a', OPTIONS);

  assert.equal(outcome.engine, 'lightweight');
  assert.equal(outcome.upgraded, false);
});

test('auto mode keeps rich lightweight results as-is', async () => {
  let renderCalls = 0;
  const engine = createEngine(testConfig(), {
    fetchPage: fakeFetch(articleHtml(150)),
    renderPage: async () => {
      renderCalls += 1;
      throw new Error('must not be called');
    },
  });

  const outcome = await engine.readPage('https://example.com/a', OPTIONS);

  assert.equal(outcome.engine, 'lightweight');
  assert.equal(renderCalls, 0);
});

test('auto mode upgrades thin lightweight results to the browser engine', async () => {
  const engine = createEngine(testConfig(), {
    fetchPage: fakeFetch(articleHtml(20, 'Shell')),
    renderPage: fakeRender(articleHtml(160, 'Full Article')),
  });

  const outcome = await engine.readPage('https://example.com/a', OPTIONS);

  assert.equal(outcome.engine, 'browser');
  assert.equal(outcome.upgraded, true);
  assert.equal(outcome.page.title, 'Full Article');
  assert.ok(outcome.page.wordCount >= 100);
});

test('auto mode keeps the lightweight result when the browser render is worse', async () => {
  const engine = createEngine(testConfig(), {
    fetchPage: fakeFetch(articleHtml(80, 'Shell')),
    renderPage: fakeRender(articleHtml(10, 'Worse')),
  });

  const outcome = await engine.readPage('https://example.com/a', OPTIONS);

  assert.equal(outcome.engine, 'lightweight');
  assert.equal(outcome.upgraded, false);
  assert.equal(outcome.page.title, 'Shell');
});

test('auto mode upgrades after parse failures and prefers the browser result', async () => {
  const engine = createEngine(testConfig(), {
    // A JS-only page: the static HTML has no extractable content.
    fetchPage: fakeFetch('<html><body><div id="app"></div></body></html>'),
    renderPage: fakeRender(articleHtml(120, 'Rendered')),
  });

  const outcome = await engine.readPage('https://example.com/a', OPTIONS);

  assert.equal(outcome.engine, 'browser');
  assert.equal(outcome.page.title, 'Rendered');
});

test('auto mode degrades to the lightweight result when the browser engine is unavailable', async () => {
  const engine = createEngine(testConfig(), {
    fetchPage: fakeFetch(articleHtml(20, 'Shell')),
    renderPage: failing(forbiddenRender),
  });

  const outcome = await engine.readPage('https://example.com/a', OPTIONS);

  assert.equal(outcome.engine, 'lightweight');
  assert.equal(outcome.page.title, 'Shell');
});

test('auto mode retries recoverable upstream failures through the browser', async () => {
  const engine = createEngine(testConfig(), {
    // A bot wall: plain fetch rejected with 403.
    fetchPage: failing(() => {
      throw new ReaderError('upstream_failed', 'upstream returned status 403');
    }),
    renderPage: fakeRender(articleHtml(150, 'Unwalled')),
  });

  const outcome = await engine.readPage('https://example.com/a', OPTIONS);

  assert.equal(outcome.engine, 'browser');
  assert.equal(outcome.page.title, 'Unwalled');
});

test('auto mode surfaces the original error when both engines fail', async () => {
  const engine = createEngine(testConfig(), {
    fetchPage: failing(() => {
      throw new ReaderError('upstream_failed', 'upstream returned status 403');
    }),
    renderPage: failing(() => {
      throw new ReaderError('upstream_failed', 'upstream returned status 503');
    }),
  });

  await assert.rejects(
    engine.readPage('https://example.com/a', OPTIONS),
    (err: unknown) => err instanceof ReaderError && err.code === 'upstream_failed' && err.message.includes('403'),
  );
});

test('auto mode does not retry timeouts or security verdicts', async () => {
  let renderCalls = 0;
  const spyRender = async (): Promise<RenderedPage> => {
    renderCalls += 1;
    throw new Error('must not be called');
  };

  const timeoutEngine = createEngine(testConfig(), {
    fetchPage: failing(() => {
      throw new ReaderError('upstream_failed', 'upstream timed out after 20000ms');
    }),
    renderPage: spyRender,
  });
  await assert.rejects(
    timeoutEngine.readPage('https://example.com/a', OPTIONS),
    (err: unknown) => err instanceof ReaderError && err.message.includes('timed out'),
  );

  const unsafeEngine = createEngine(testConfig(), {
    fetchPage: failing(() => {
      throw new ReaderError('unsafe_destination', 'destination 10.0.0.1 is a non-public address');
    }),
    renderPage: spyRender,
  });
  await assert.rejects(
    unsafeEngine.readPage('https://example.com/a', OPTIONS),
    (err: unknown) => err instanceof ReaderError && err.code === 'unsafe_destination',
  );

  assert.equal(renderCalls, 0);
});

test('browser mode always renders and never upgrades', async () => {
  const engine = createEngine(testConfig({ engine: 'browser' }), {
    fetchPage: failing(() => {
      throw new Error('lightweight fetch must not be called');
    }),
    renderPage: fakeRender(articleHtml(130, 'Rendered')),
  });

  const outcome = await engine.readPage('https://example.com/a', OPTIONS);

  assert.equal(outcome.engine, 'browser');
  assert.equal(outcome.upgraded, false);
});

test('browser mode propagates engine_unavailable', async () => {
  const engine = createEngine(testConfig({ engine: 'browser' }), {
    renderPage: failing(forbiddenRender),
  });

  await assert.rejects(
    engine.readPage('https://example.com/a', OPTIONS),
    (err: unknown) => err instanceof ReaderError && err.code === 'engine_unavailable',
  );
});

test('busy browser slots degrade in auto mode but fail in browser mode', async () => {
  const release = { fn: null as (() => void) | null };
  const heldRender = (): Promise<RenderedPage> =>
    new Promise((resolve) => {
      release.fn = () => resolve({
        finalUrl: 'https://example.com/a',
        status: 200,
        contentType: 'text/html',
        html: articleHtml(150),
        bytes: 100,
        redirects: 0,
      });
    });

  // auto: second request degrades to the (thin) lightweight result.
  const autoEngine = createEngine(testConfig({ browserMaxConcurrent: 1 }), {
    fetchPage: fakeFetch(articleHtml(20, 'Shell')),
    renderPage: heldRender,
  });
  const first = autoEngine.readPage('https://example.com/a', OPTIONS);
  await new Promise((resolve) => setTimeout(resolve, 20));
  const second = await autoEngine.readPage('https://example.com/a', OPTIONS);
  assert.equal(second.engine, 'lightweight');
  release.fn?.();
  const firstOutcome = await first;
  assert.equal(firstOutcome.engine, 'browser');

  // browser mode: concurrency limit is a hard 503.
  const browserEngine = createEngine(testConfig({ engine: 'browser', browserMaxConcurrent: 1 }), {
    renderPage: heldRender,
  });
  const held = browserEngine.readPage('https://example.com/a', OPTIONS);
  await new Promise((resolve) => setTimeout(resolve, 20));
  await assert.rejects(
    browserEngine.readPage('https://example.com/a', OPTIONS),
    (err: unknown) => err instanceof ReaderError && err.code === 'service_busy',
  );
  release.fn?.();
  await held;
});
