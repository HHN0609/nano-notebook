# Multi-stage build for the web-reader sidecar (TypeScript / Node.js).
# Pinned base images, non-root runtime, healthcheck against /health/live.
#
# The image ships a system Chromium for the optional browser rendering
# engine (puppeteer-core drives it; no bundled download). Deployments that
# only need the lightweight engine still work unchanged — the browser is
# launched lazily on the first browser/auto-engine request.

FROM node:22-bookworm-slim AS build

WORKDIR /src
COPY services/web-reader/package.json services/web-reader/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY services/web-reader/tsconfig.json ./
COPY services/web-reader/src ./src
RUN npm run build

FROM node:22-bookworm-slim AS prune

WORKDIR /src
COPY services/web-reader/package.json services/web-reader/package-lock.json ./
RUN npm ci --omit=dev --no-audit --no-fund

FROM node:22-bookworm-slim

# Chromium + minimal fonts for the browser rendering engine.
RUN apt-get update \
  && apt-get install -y --no-install-recommends chromium fonts-dejavu-core ca-certificates \
  && rm -rf /var/lib/apt/lists/*

# The container runs read-only with a tmpfs scratch, so Chromium's profile
# and cache must live in /tmp (HOME is honored by the launch options).
ENV NODE_ENV=production \
    HOME=/tmp \
    NANO_WEB_READER_BROWSER_EXECUTABLE=/usr/bin/chromium

WORKDIR /app
COPY --from=prune /src/node_modules ./node_modules
COPY --from=build /src/dist/src ./dist
COPY services/web-reader/package.json ./

USER 65532:65532
EXPOSE 8085
HEALTHCHECK --interval=10s --timeout=3s --retries=20 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:8085/health/live').then((r) => { if (!r.ok) process.exit(1); }).catch(() => process.exit(1))"]
ENTRYPOINT ["node", "dist/index.js"]
