/**
 * Engine orchestration, following the jina-ai/reader `x-engine` model:
 *
 *   lightweight — plain HTTP fetch (src/fetcher.ts) + static parsing only.
 *   browser     — always render with the browser engine (src/browser.ts).
 *   auto        — lightweight first, then upgrade to the browser engine
 *                 when the result looks wrong: parsing failed, the upstream
 *                 rejected the plain fetch (bot walls) or the extracted
 *                 content is too thin to trust.
 *
 * In auto mode every browser failure (engine unavailable, busy slots,
 * render errors) degrades gracefully to the lightweight outcome.
 */

import { fetchPage } from './fetcher.js';
import { renderPage, closeBrowser, type RenderedPage } from './browser.js';
import { parsePage, type ParseOptions, type ParseResult } from './reader.js';
import { ReaderError } from './errors.js';
import type { Config } from './config.js';

export type EngineKind = 'lightweight' | 'browser';

export interface FetchSummary {
  status: number;
  content_type: string;
  charset: string;
  bytes: number;
  redirects: number;
}

export interface ReadOutcome {
  engine: EngineKind;
  /** True when auto mode upgraded a lightweight attempt to a browser render. */
  upgraded: boolean;
  finalUrl: string;
  page: ParseResult;
  fetch: FetchSummary;
}

export interface EngineDeps {
  fetchPage: typeof fetchPage;
  renderPage: typeof renderPage;
}

export interface Engine {
  readPage(url: string, options: ParseOptions): Promise<ReadOutcome>;
  close(): Promise<void>;
}

class Semaphore {
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

export function createEngine(config: Config, deps: Partial<EngineDeps> = {}): Engine {
  const doFetch = deps.fetchPage ?? fetchPage;
  const doRender = deps.renderPage ?? renderPage;
  const browserGate = new Semaphore(config.browserMaxConcurrent);
  let warnedUnavailable = false;

  async function runLightweight(url: string, options: ParseOptions): Promise<ReadOutcome> {
    const fetched = await doFetch(url, config);
    const page = parsePage(fetched.body, fetched.finalUrl, options);
    return {
      engine: 'lightweight',
      upgraded: false,
      finalUrl: fetched.finalUrl,
      page,
      fetch: {
        status: fetched.status,
        content_type: fetched.contentType,
        charset: fetched.charset,
        bytes: fetched.bytes,
        redirects: fetched.redirects,
      },
    };
  }

  async function runBrowser(url: string, options: ParseOptions): Promise<ReadOutcome> {
    const rendered = await doRender(url, config);
    const page = parsePage(rendered.html, rendered.finalUrl, options);
    return {
      engine: 'browser',
      upgraded: false,
      finalUrl: rendered.finalUrl,
      page,
      fetch: {
        status: rendered.status,
        content_type: rendered.contentType,
        charset: 'utf-8',
        bytes: rendered.bytes,
        redirects: rendered.redirects,
      },
    };
  }

  async function readPage(url: string, options: ParseOptions): Promise<ReadOutcome> {
    if (config.engine === 'browser') {
      if (!browserGate.tryAcquire()) {
        throw new ReaderError(
          'service_busy',
          `browser engine is at its concurrency limit (${config.browserMaxConcurrent})`,
        );
      }
      try {
        return await runBrowser(url, options);
      } finally {
        browserGate.release();
      }
    }

    let light: ReadOutcome | null = null;
    let lightError: unknown = null;
    try {
      light = await runLightweight(url, options);
    } catch (err) {
      lightError = err;
    }

    if (config.engine === 'lightweight') {
      if (lightError !== null) {
        throw lightError;
      }
      return light as ReadOutcome;
    }

    const wantsUpgrade =
      lightError !== null
        ? isRecoverableFailure(lightError)
        : isThinContent(light, config);

    if (!wantsUpgrade) {
      if (lightError !== null) {
        throw lightError;
      }
      return light as ReadOutcome;
    }

    if (!browserGate.tryAcquire()) {
      // All browser slots busy: degrade to the lightweight outcome.
      if (lightError !== null) {
        throw lightError;
      }
      return light as ReadOutcome;
    }

    try {
      const browserOutcome = await runBrowser(url, options);
      browserOutcome.upgraded = true;
      if (light === null || browserOutcome.page.wordCount > light.page.wordCount) {
        return browserOutcome;
      }
      return light;
    } catch (err) {
      if (err instanceof ReaderError && err.code === 'engine_unavailable' && !warnedUnavailable) {
        warnedUnavailable = true;
        console.warn(
          '[web-reader] browser engine unavailable, auto mode degrades to lightweight fetches:',
          err.message,
        );
      }
      if (lightError !== null) {
        throw lightError;
      }
      return light as ReadOutcome;
    } finally {
      browserGate.release();
    }
  }

  return {
    readPage,
    close: () => closeBrowser(),
  };
}

/**
 * Only failures a real browser could plausibly recover from: parsing
 * produced nothing, or the plain fetch was rejected (bot walls, 403s).
 * Timeouts are excluded — a slower engine will not fix a dead upstream.
 * Security verdicts (unsafe_destination) are never retried.
 */
function isRecoverableFailure(err: unknown): boolean {
  if (!(err instanceof ReaderError)) {
    return false;
  }
  if (err.code === 'parse_failed') {
    return true;
  }
  if (err.code === 'upstream_failed') {
    return !/timeout|timed out/i.test(err.message);
  }
  return false;
}

function isThinContent(outcome: ReadOutcome | null, config: Config): boolean {
  if (outcome === null) {
    return false;
  }
  return outcome.page.wordCount < config.autoUpgradeMinWords;
}
