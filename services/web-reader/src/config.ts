/**
 * Environment-driven configuration with fail-fast validation, following the
 * repo's Go sidecar conventions (`NANO_<SERVICE>_*` variables).
 */

export type EngineMode = 'lightweight' | 'browser' | 'auto';

const ENGINE_MODES: readonly EngineMode[] = ['lightweight', 'browser', 'auto'];

export interface Config {
  addr: string;
  serviceToken: string;
  maxConcurrent: number;
  fetchTimeoutMs: number;
  maxRedirects: number;
  maxResponseBytes: number;
  maxContentChars: number;
  maxRequestBodyBytes: number;
  allowPrivateTargets: boolean;
  /** Trust local DNS-proxy synthetic addresses for hostname lookups only. */
  allowSyntheticProxyTargets: boolean;
  userAgent: string;
  /** Rendering engine policy: lightweight fetch, browser render, or auto upgrade. */
  engine: EngineMode;
  browserExecutable: string;
  browserTimeoutMs: number;
  browserMaxConcurrent: number;
  /** In auto mode, lightweight results below this word count trigger a browser retry. */
  autoUpgradeMinWords: number;
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const addr = env['NANO_WEB_READER_ADDR'] ?? '127.0.0.1:8085';
  if (!/^[^/\s]*:\d+$/.test(addr)) {
    throw new Error(`invalid NANO_WEB_READER_ADDR: ${addr}`);
  }

  const serviceToken = env['NANO_WEB_READER_SERVICE_TOKEN'] ?? '';

  const maxConcurrent = parsePositiveInt(env['NANO_WEB_READER_MAX_CONCURRENT'], 8, 'NANO_WEB_READER_MAX_CONCURRENT');
  const fetchTimeoutMs = parsePositiveInt(env['NANO_WEB_READER_FETCH_TIMEOUT_MS'], 20_000, 'NANO_WEB_READER_FETCH_TIMEOUT_MS');
  const maxRedirects = parsePositiveInt(env['NANO_WEB_READER_MAX_REDIRECTS'], 5, 'NANO_WEB_READER_MAX_REDIRECTS');
  const maxResponseBytes = parsePositiveInt(env['NANO_WEB_READER_MAX_RESPONSE_BYTES'], 5 * 1024 * 1024, 'NANO_WEB_READER_MAX_RESPONSE_BYTES');
  const maxContentChars = parsePositiveInt(env['NANO_WEB_READER_MAX_CONTENT_CHARS'], 250_000, 'NANO_WEB_READER_MAX_CONTENT_CHARS');
  const maxRequestBodyBytes = parsePositiveInt(env['NANO_WEB_READER_MAX_REQUEST_BODY_BYTES'], 16 * 1024, 'NANO_WEB_READER_MAX_REQUEST_BODY_BYTES');

  const allowPrivateTargets = (env['NANO_WEB_READER_ALLOW_PRIVATE_TARGETS'] ?? 'false').toLowerCase() === 'true';
  const allowSyntheticProxyTargets = (env['NANO_WEB_READER_ALLOW_SYNTHETIC_PROXY_TARGETS'] ?? 'false').toLowerCase() === 'true';

  // Default to a realistic Edge UA (Windows 10, Chromium 138, Edg 151) so
  // weakly-protected sites do not reject us out of hand; strong anti-bot
  // walls (zhihu etc.) are handled by the browser engine, not by UA alone.
  const userAgent =
    env['NANO_WEB_READER_USER_AGENT'] ??
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36 Edg/151.0.0.0';

  const engineRaw = (env['NANO_WEB_READER_ENGINE'] ?? 'auto').toLowerCase();
  if (!ENGINE_MODES.includes(engineRaw as EngineMode)) {
    throw new Error(`invalid NANO_WEB_READER_ENGINE: ${engineRaw} (expected ${ENGINE_MODES.join(', ')})`);
  }

  const browserExecutable = env['NANO_WEB_READER_BROWSER_EXECUTABLE'] ?? '';
  const browserTimeoutMs = parsePositiveInt(env['NANO_WEB_READER_BROWSER_TIMEOUT_MS'], 30_000, 'NANO_WEB_READER_BROWSER_TIMEOUT_MS');
  const browserMaxConcurrent = parsePositiveInt(env['NANO_WEB_READER_BROWSER_MAX_CONCURRENT'], 2, 'NANO_WEB_READER_BROWSER_MAX_CONCURRENT');
  const autoUpgradeMinWords = parsePositiveInt(env['NANO_WEB_READER_AUTO_UPGRADE_MIN_WORDS'], 100, 'NANO_WEB_READER_AUTO_UPGRADE_MIN_WORDS');

  return {
    addr,
    serviceToken,
    maxConcurrent,
    fetchTimeoutMs,
    maxRedirects,
    maxResponseBytes,
    maxContentChars,
    maxRequestBodyBytes,
    allowPrivateTargets,
    allowSyntheticProxyTargets,
    userAgent,
    engine: engineRaw as EngineMode,
    browserExecutable,
    browserTimeoutMs,
    browserMaxConcurrent,
    autoUpgradeMinWords,
  };
}

function parsePositiveInt(raw: string | undefined, fallback: number, name: string): number {
  if (raw === undefined || raw === '') {
    return fallback;
  }
  const value = Number(raw);
  if (!Number.isInteger(value) || value <= 0) {
    throw new Error(`invalid ${name}: ${raw}`);
  }
  return value;
}
