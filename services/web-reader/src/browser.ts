/**
 * Browser rendering engine (optional capability). Renders JS-heavy pages
 * with a shared headless Chromium instance driven by puppeteer-core, which
 * is the same approach as the jina-ai/reader browser engine.
 *
 * SSRF posture: unlike the lightweight fetcher (which dials only
 * pre-validated IPs itself), Chromium resolves DNS on its own. We therefore
 * (1) pre-validate the initial URL before launching a page, and
 * (2) install a request interceptor that resolves every requested hostname
 * (main frame, every redirect hop and subresources) through the same
 * public-address rules before Chromium is allowed to connect. Verdicts are
 * cached per render only, to avoid cross-request DNS-rebinding windows.
 * The residual TOCTOU window between our lookup and Chromium's own
 * resolution is accepted here and covered by container egress policy in
 * production (ADR-0032 stance).
 */

import fs from 'node:fs';
import { promises as dns } from 'node:dns';
import { isIPv4, isIPv6 } from 'node:net';
import puppeteer, { type Browser, type HTTPRequest, type HTTPResponse } from 'puppeteer-core';

import { ReaderError } from './errors.js';
import { isIPInNonPublicRange, isSyntheticProxyAddress } from './ip.js';
import { validateUrl } from './fetcher.js';
import type { Config } from './config.js';

export interface RenderedPage {
  finalUrl: string;
  status: number;
  contentType: string;
  html: string;
  bytes: number;
  redirects: number;
}

const HTML_CONTENT_TYPES = new Set(['text/html', 'application/xhtml+xml']);

const EXECUTABLE_CANDIDATES = [
  process.env['PUPPETEER_EXECUTABLE_PATH'],
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
  '/snap/bin/chromium',
];

export function resolveBrowserExecutable(config: Config): string {
  if (config.browserExecutable !== '') {
    return config.browserExecutable;
  }
  for (const candidate of EXECUTABLE_CANDIDATES) {
    if (candidate !== undefined && candidate !== '' && fs.existsSync(candidate)) {
      return candidate;
    }
  }
  throw new ReaderError(
    'engine_unavailable',
    'no browser executable found; set NANO_WEB_READER_BROWSER_EXECUTABLE or install chromium',
  );
}

async function launchBrowser(config: Config): Promise<Browser> {
  const executablePath = resolveBrowserExecutable(config);
  return puppeteer.launch({
    executablePath,
    headless: true,
    env: { ...process.env, HOME: '/tmp' },
    args: [
      // The sidecar container drops all capabilities, so Chromium's setuid
      // sandbox cannot work; isolation comes from the container itself
      // (read-only rootfs, cap_drop, pids/memory limits, egress policy).
      '--no-sandbox',
      '--disable-setuid-sandbox',
      '--disable-dev-shm-usage',
      '--disable-gpu',
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-background-networking',
      '--disable-sync',
      '--mute-audio',
      '--disable-features=Translate',
      // Anti-bot walls (zhihu etc.) fingerprint navigator.webdriver; the
      // blink feature flag keeps it false under CDP control.
      '--disable-blink-features=AutomationControlled',
    ],
  });
}

let activeBrowser: Browser | null = null;
let launching: Promise<Browser> | null = null;

export async function ensureBrowser(config: Config): Promise<Browser> {
  if (activeBrowser?.connected) {
    return activeBrowser;
  }
  activeBrowser = null;
  if (launching === null) {
    launching = launchBrowser(config).finally(() => {
      launching = null;
    });
  }
  const browser = await launching;
  browser.once('disconnected', () => {
    if (activeBrowser === browser) {
      activeBrowser = null;
    }
  });
  activeBrowser = browser;
  return browser;
}

export async function closeBrowser(): Promise<void> {
  const browser = activeBrowser;
  activeBrowser = null;
  if (browser?.connected) {
    await browser.close().catch(() => {});
  }
}

export async function renderPage(rawUrl: string, config: Config): Promise<RenderedPage> {
  const target = validateUrl(rawUrl);
  if (!config.allowPrivateTargets) {
    await assertPublicHost(target.hostname.replace(/^\[|\]$/g, ''), config);
  }

  const browser = await ensureBrowser(config);
  const page = await browser.newPage();
  try {
    // setUserAgent without userAgentMetadata makes Chromium drop the
    // sec-ch-ua* client-hint headers on HTTPS requests; real browsers
    // always send them, so anti-bot walls (zhihu etc.) reject the mismatch.
    await page.setUserAgent({
      userAgent: config.userAgent,
      userAgentMetadata: buildUserAgentMetadata(config.userAgent),
    });
    await page.setViewport({ width: 1280, height: 800 });
    await installSsrfGuard(page, config);

    // Anti-bot challenge walls (zhihu zse-ck etc.) answer the first
    // navigation with 4xx plus a JS challenge that sets a cookie and
    // reloads; the real content only arrives on a later main-frame
    // navigation. Track the latest main-frame response and judge after
    // the settle window instead of failing on the first 4xx.
    let lastNavResponse: HTTPResponse | null = null;
    page.on('response', (res) => {
      const req = res.request();
      if (req.isNavigationRequest() && req.frame() === page.mainFrame()) {
        lastNavResponse = res;
      }
    });

    let response;
    try {
      response = await page.goto(target.toString(), {
        waitUntil: 'domcontentloaded',
        timeout: config.browserTimeoutMs,
      });
    } catch (err) {
      throw classifyNavigationError(err, config.browserTimeoutMs);
    }

    // Best-effort settle time for JS frameworks hydrating, XHR bursts and
    // challenge-driven reloads; timeouts here are not fatal, the DOM
    // snapshot is taken either way.
    await page
      .waitForNetworkIdle({ idleTime: 800, timeout: Math.min(config.browserTimeoutMs, 10_000) })
      .catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 300));

    const finalResponse = lastNavResponse ?? response;
    if (!finalResponse) {
      throw new ReaderError('upstream_failed', 'browser navigation produced no response');
    }

    const status = finalResponse.status();
    if (status >= 400) {
      throw new ReaderError('upstream_failed', `upstream returned status ${status}`);
    }

    const contentType = finalResponse.headers()['content-type'] ?? '';
    const mediaType = contentType.split(';')[0]?.trim().toLowerCase() ?? '';
    if (!HTML_CONTENT_TYPES.has(mediaType)) {
      throw new ReaderError('unsupported_type', `unsupported content type: ${contentType || '(none)'}`);
    }

    const finalUrl = validateUrl(page.url());
    const html = await page.content();
    const redirects = finalResponse.request().redirectChain().length;

    return {
      finalUrl: finalUrl.toString(),
      status,
      contentType,
      html,
      bytes: Buffer.byteLength(html),
      redirects,
    };
  } finally {
    await page.close().catch(() => {});
  }
}

/** Pre-flight DNS validation of the initial navigation target. */
async function assertPublicHost(host: string, config: Config): Promise<void> {
  if (isIPv4(host) || isIPv6(host)) {
    if (isIPInNonPublicRange(host)) {
      throw new ReaderError('unsafe_destination', `destination ${host} is a non-public address`);
    }
    return;
  }
  let addresses: { address: string }[];
  try {
    addresses = await dns.lookup(host, { all: true });
  } catch {
    throw new ReaderError('upstream_failed', `dns resolution failed for ${host}`);
  }
  if (addresses.length === 0) {
    throw new ReaderError('upstream_failed', `dns resolution failed for ${host}`);
  }
  for (const addr of addresses) {
    if (isIPInNonPublicRange(addr.address) &&
        !(config.allowSyntheticProxyTargets && isSyntheticProxyAddress(addr.address))) {
      throw new ReaderError(
        'unsafe_destination',
        `destination ${host} resolves to non-public address ${addr.address}`,
      );
    }
  }
}

async function installSsrfGuard(page: import('puppeteer-core').Page, config: Config): Promise<void> {
  await page.setRequestInterception(true);
  // Verdicts are cached per render: the cache dies with the page, so a
  // hostname re-validated for the next render cannot be rebinding-window
  // reused from a previous one.
  const verdicts = new Map<string, boolean>();
  page.on('request', (req: HTTPRequest) => {
    void (async () => {
      let allowed = false;
      try {
        allowed = await checkRequestUrl(req.url(), config, verdicts);
      } catch {
        allowed = false;
      }
      try {
        if (allowed) {
          await req.continue();
        } else {
          await req.abort('accessdenied');
        }
      } catch {
        // The request was already settled (navigation cancelled); ignore.
      }
    })();
  });
}

/** Exported for tests: decide whether Chromium may connect to a request URL. */
export async function checkRequestUrl(
  rawUrl: string,
  config: Config,
  verdicts: Map<string, boolean> = new Map(),
): Promise<boolean> {
  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    return false;
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return false;
  }
  if (url.username !== '' || url.password !== '') {
    return false;
  }
  if (config.allowPrivateTargets) {
    return true;
  }
  const host = url.hostname.replace(/^\[|\]$/g, '');
  if (isIPv4(host) || isIPv6(host)) {
    return !isIPInNonPublicRange(host);
  }
  const cached = verdicts.get(host);
  if (cached !== undefined) {
    return cached;
  }
  let ok = false;
  try {
    const addresses = await dns.lookup(host, { all: true });
    ok = addresses.length > 0 && addresses.every((addr) =>
      !isIPInNonPublicRange(addr.address) ||
      (config.allowSyntheticProxyTargets && isSyntheticProxyAddress(addr.address))
    );
  } catch {
    ok = false;
  }
  verdicts.set(host, ok);
  return ok;
}

/**
 * Derive client-hints metadata consistent with a UA string, so the browser
 * keeps sending realistic `sec-ch-ua*` headers matching the overridden UA.
 */
export function buildUserAgentMetadata(userAgent: string): UserAgentMetadata {
  const chromiumVersion = /Chrome\/(\d+(?:\.\d+)*)/.exec(userAgent)?.[1] ?? '138.0.0.0';
  const chromiumMajor = chromiumVersion.split('.')[0] ?? '138';
  const edgeMajor = /Edg\w*\/(\d+)/.exec(userAgent)?.[1];

  const brands = edgeMajor
    ? [
        { brand: 'Microsoft Edge', version: edgeMajor },
        { brand: 'Chromium', version: chromiumMajor },
        { brand: 'Not=A?Brand', version: '24' },
      ]
    : [
        { brand: 'Chromium', version: chromiumMajor },
        { brand: 'Not=A?Brand', version: '24' },
      ];

  const platform = /Windows/i.test(userAgent)
    ? 'Windows'
    : /Macintosh|Mac OS X/i.test(userAgent)
      ? 'macOS'
      : /Linux|X11|Ubuntu/i.test(userAgent)
        ? 'Linux'
        : 'Windows';

  const fullVersionList = brands.map((brand) => ({
    brand: brand.brand,
    version: brand.brand === 'Chromium' ? chromiumVersion : `${brand.version}.0.0.0`,
  }));

  return {
    brands,
    fullVersionList,
    fullVersion: chromiumVersion,
    platform,
    platformVersion: platform === 'Windows' ? '13.0.0' : platform === 'macOS' ? '14.5.0' : '',
    architecture: 'x86',
    model: '',
    mobile: false,
    bitness: '64',
    wow64: false,
  };
}

type UserAgentMetadata = import('puppeteer-core').Protocol.Emulation.UserAgentMetadata;

function classifyNavigationError(err: unknown, timeoutMs: number): ReaderError {
  const message = err instanceof Error ? err.message : String(err);
  if (message.includes('ERR_ACCESS_DENIED')) {
    return new ReaderError('unsafe_destination', 'destination rejected by the ssrf request guard');
  }
  if (message.includes('ERR_NAME_NOT_RESOLVED')) {
    return new ReaderError('upstream_failed', `dns resolution failed: ${message}`);
  }
  if (/timeout|timed out|TIMED_OUT/i.test(message)) {
    return new ReaderError('upstream_failed', `upstream timed out after ${timeoutMs}ms`);
  }
  return new ReaderError('upstream_failed', `browser navigation failed: ${message}`);
}
