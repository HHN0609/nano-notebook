/**
 * web-reader service entrypoint: load configuration, start the HTTP server,
 * and shut down gracefully on SIGTERM/SIGINT (including the shared browser
 * instance when the rendering engine is enabled).
 */

import { createReaderServer } from './server.js';
import { closeBrowser } from './browser.js';
import { loadConfig } from './config.js';

const config = loadConfig();
const server = createReaderServer(config);

const [host, port] = splitAddr(config.addr);

server.listen(port, host, () => {
  const engineNote =
    config.engine === 'browser'
      ? 'browser engine'
      : config.engine === 'auto'
        ? 'auto engine (lightweight with browser upgrade)'
        : 'lightweight engine';
  console.log(
    `[web-reader] listening on ${config.addr} (${engineNote}, concurrency ${config.maxConcurrent}/${config.browserMaxConcurrent} browser, fetch budgets html=${config.maxResponseBytes} pdf=${config.maxPdfResponseBytes} bytes / ${config.fetchTimeoutMs}ms)`,
  );
});

let shuttingDown = false;
for (const signal of ['SIGTERM', 'SIGINT'] as const) {
  process.on(signal, () => {
    if (shuttingDown) return;
    shuttingDown = true;
    console.log(`[web-reader] received ${signal}, shutting down`);
    server.close(() => {
      void closeBrowser().finally(() => process.exit(0));
    });
    server.closeIdleConnections();
    setTimeout(() => {
      console.error('[web-reader] forced shutdown after grace period');
      server.closeAllConnections();
      void closeBrowser().finally(() => process.exit(0));
    }, 5000).unref();
  });
}

function splitAddr(addr: string): [string, number] {
  const index = addr.lastIndexOf(':');
  const host = addr.slice(0, index);
  const port = Number(addr.slice(index + 1));
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`invalid listen address: ${addr}`);
  }
  return [host === '' ? '0.0.0.0' : host, port];
}
