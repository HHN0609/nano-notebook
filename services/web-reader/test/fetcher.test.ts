import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import zlib from 'node:zlib';

import { fetchPage, fetchResource, validateUrl } from '../src/fetcher.js';
import { ReaderError } from '../src/errors.js';
import type { Config } from '../src/config.js';

const HTML = '<html><head><title>Fixture</title></head><body><p>hello world</p></body></html>';

function baseConfig(overrides: Partial<Config> = {}): Config {
  return {
    addr: '127.0.0.1:0',
    serviceToken: '',
    maxConcurrent: 4,
    fetchTimeoutMs: 3_000,
    maxRedirects: 5,
    maxResponseBytes: 1024 * 1024,
    maxPdfResponseBytes: 20 * 1024 * 1024,
    maxContentChars: 250_000,
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

const PDF = Buffer.from('%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n', 'ascii');

interface Fixture {
  url: (path: string) => string;
  close: () => Promise<void>;
}

async function startFixture(handler: http.RequestListener): Promise<Fixture> {
  const server = http.createServer(handler);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  return {
    url: (path: string) => `http://127.0.0.1:${port}${path}`,
    close: () => new Promise((resolve) => server.close(() => resolve())),
  };
}

test('validateUrl rejects non-http schemes, userinfo and fragments', () => {
  for (const raw of ['ftp://example.com/x', 'file:///etc/passwd', 'javascript:alert(1)']) {
    assert.throws(
      () => validateUrl(raw),
      (err: unknown) => err instanceof ReaderError && err.code === 'unsafe_destination',
      raw,
    );
  }
  assert.throws(
    () => validateUrl('https://user:pass@example.com/'),
    (err: unknown) => err instanceof ReaderError && err.code === 'unsafe_destination',
  );
  assert.throws(
    () => validateUrl('https://example.com/#section'),
    (err: unknown) => err instanceof ReaderError && err.code === 'unsafe_destination',
  );
  assert.throws(
    () => validateUrl('not a url'),
    (err: unknown) => err instanceof ReaderError && err.code === 'invalid_request',
  );
});

test('fetchPage fetches an html page and decodes the body', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    res.end(HTML);
  });
  try {
    const result = await fetchPage(fixture.url('/page'), baseConfig());

    assert.equal(result.status, 200);
    assert.equal(result.finalUrl, fixture.url('/page'));
    assert.equal(result.charset, 'utf-8');
    assert.ok(result.body.includes('hello world'));
    assert.equal(result.redirects, 0);
  } finally {
    await fixture.close();
  }
});

test('fetchPage resolves hostnames through the validating lookup', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    res.end(HTML);
  });
  try {
    // IP literals connect directly and never exercise the custom `lookup`
    // hook; `localhost` does, covering the all-mode callback contract that
    // net relies on when autoSelectFamily (Happy Eyeballs) is enabled.
    const result = await fetchPage(fixture.url('/page').replace('127.0.0.1', 'localhost'), baseConfig());

    assert.equal(result.status, 200);
    assert.ok(result.body.includes('hello world'));
  } finally {
    await fixture.close();
  }
});

test('fetchPage follows redirects and reports the final url', async () => {
  const fixture = await startFixture((req, res) => {
    if (req.url === '/a') {
      res.writeHead(302, { location: '/b' });
      res.end();
      return;
    }
    if (req.url === '/b') {
      res.writeHead(301, { location: '/c' });
      res.end();
      return;
    }
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(HTML);
  });
  try {
    const result = await fetchPage(fixture.url('/a'), baseConfig());

    assert.equal(result.redirects, 2);
    assert.equal(result.finalUrl, fixture.url('/c'));
  } finally {
    await fixture.close();
  }
});

test('fetchPage enforces the redirect budget', async () => {
  const fixture = await startFixture((req, res) => {
    res.writeHead(302, { location: '/next' });
    res.end();
  });
  try {
    await assert.rejects(
      fetchPage(fixture.url('/start'), baseConfig({ maxRedirects: 2 })),
      (err: unknown) => err instanceof ReaderError && err.code === 'upstream_failed',
    );
  } finally {
    await fixture.close();
  }
});

test('fetchPage blocks private destinations unless explicitly allowed', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(HTML);
  });
  try {
    await assert.rejects(
      fetchPage(fixture.url('/page'), baseConfig({ allowPrivateTargets: false })),
      (err: unknown) => err instanceof ReaderError && err.code === 'unsafe_destination',
    );
  } finally {
    await fixture.close();
  }
});

test('fetchPage re-validates every redirect hop', async () => {
  const fixture = await startFixture((req, res) => {
    if (req.url === '/redirect') {
      res.writeHead(302, { location: 'ftp://example.com/target' });
      res.end();
      return;
    }
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(HTML);
  });
  try {
    // Every redirect target passes through the same URL validation as the
    // initial request, so a scheme downgrade is rejected.
    await assert.rejects(
      fetchPage(fixture.url('/redirect'), baseConfig({ allowPrivateTargets: true })),
      (err: unknown) => err instanceof ReaderError && err.code === 'unsafe_destination',
    );
  } finally {
    await fixture.close();
  }
});

test('fetchPage enforces the response size budget', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end('x'.repeat(5000));
  });
  try {
    await assert.rejects(
      fetchPage(fixture.url('/page'), baseConfig({ maxResponseBytes: 1024 })),
      (err: unknown) => err instanceof ReaderError && err.code === 'response_too_large',
    );
  } finally {
    await fixture.close();
  }
});

test('fetchPage rejects non-html content types', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end('{}');
  });
  try {
    await assert.rejects(
      fetchPage(fixture.url('/page'), baseConfig()),
      (err: unknown) => err instanceof ReaderError && err.code === 'unsupported_type',
    );
  } finally {
    await fixture.close();
  }
});

test('fetchResource acquires bounded PDF bytes without decoding them as text', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/pdf' });
    res.end(PDF);
  });
  try {
    const result = await fetchResource(fixture.url('/paper'), baseConfig());

    assert.equal(result.mediaType, 'pdf');
    assert.equal(result.contentType, 'application/pdf');
    assert.equal(result.finalUrl, fixture.url('/paper'));
    assert.deepEqual(result.body, PDF);
  } finally {
    await fixture.close();
  }
});

test('fetchResource requires PDF content type and signature to agree', async () => {
  const badPdf = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/pdf' });
    res.end('<html>not a pdf</html>');
  });
  const disguisedPdf = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(PDF);
  });
  try {
    await assert.rejects(
      fetchResource(badPdf.url('/paper'), baseConfig()),
      (err: unknown) => err instanceof ReaderError && err.code === 'document_type_mismatch' && /signature/i.test(err.message),
    );
    await assert.rejects(
      fetchResource(disguisedPdf.url('/paper'), baseConfig()),
      (err: unknown) => err instanceof ReaderError && err.code === 'document_type_mismatch' && /content type/i.test(err.message),
    );
  } finally {
    await badPdf.close();
    await disguisedPdf.close();
  }
});

test('fetchResource applies the separate PDF byte budget', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/pdf' });
    res.end(Buffer.concat([PDF, Buffer.alloc(2048)]));
  });
  try {
    await assert.rejects(
      fetchResource(fixture.url('/paper'), baseConfig({ maxPdfResponseBytes: 1024 })),
      (err: unknown) => err instanceof ReaderError && err.code === 'response_too_large',
    );
  } finally {
    await fixture.close();
  }
});

test('fetchPage remains HTML-only when the upstream is a PDF', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/pdf' });
    res.end(PDF);
  });
  try {
    await assert.rejects(
      fetchPage(fixture.url('/paper'), baseConfig()),
      (err: unknown) => err instanceof ReaderError && err.code === 'unsupported_type',
    );
  } finally {
    await fixture.close();
  }
});

test('fetchPage rejects PDF from headers before applying the PDF body budget', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/pdf' });
    res.end(Buffer.concat([PDF, Buffer.alloc(2048)]));
  });
  try {
    await assert.rejects(
      fetchPage(fixture.url('/paper'), baseConfig({ maxPdfResponseBytes: 16 })),
      (err: unknown) => err instanceof ReaderError && err.code === 'unsupported_type',
    );
  } finally {
    await fixture.close();
  }
});

test('fetchPage surfaces upstream failures', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(404, { 'content-type': 'text/html' });
    res.end('<html><body>missing</body></html>');
  });
  try {
    await assert.rejects(
      fetchPage(fixture.url('/page'), baseConfig()),
      (err: unknown) => err instanceof ReaderError && err.code === 'upstream_failed',
    );
  } finally {
    await fixture.close();
  }
});

test('fetchPage decompresses gzip responses', async () => {
  const fixture = await startFixture((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'content-encoding': 'gzip' });
    res.end(zlib.gzipSync(Buffer.from(HTML, 'utf8')));
  });
  try {
    const result = await fetchPage(fixture.url('/page'), baseConfig());

    assert.ok(result.body.includes('hello world'));
  } finally {
    await fixture.close();
  }
});

test('fetchPage decodes non-utf8 charsets sniffed from meta tags', async () => {
  const fixture = await startFixture((_req, res) => {
    const body = Buffer.concat([
      Buffer.from('<html><head><meta charset="gb2312"><title>', 'latin1'),
      Buffer.from([0xd6, 0xd0, 0xce, 0xc4, 0xb2, 0xe2, 0xca, 0xd4]), // 中文测试
      Buffer.from('</title></head><body><p>', 'latin1'),
      Buffer.from([0xd6, 0xd0, 0xce, 0xc4]), // 中文
      Buffer.from('</p></body></html>', 'latin1'),
    ]);
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(body);
  });
  try {
    const result = await fetchPage(fixture.url('/page'), baseConfig());

    assert.equal(result.charset, 'gb2312');
    assert.ok(result.body.includes('中文测试'));
  } finally {
    await fixture.close();
  }
});
