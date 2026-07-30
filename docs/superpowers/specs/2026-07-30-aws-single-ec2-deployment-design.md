# AWS Single-EC2 Deployment Design

## Goal

Deploy Nano Notebook to a single AWS EC2 instance with Docker Compose. Minimal
complexity — no scaling, no managed services, just run and use.

## Architecture

Single EC2 (t3.medium, Ubuntu 22.04) running all services via Docker Compose:

```
EC2
├── nginx                     → 反向代理 + 前端 SPA 静态文件 (:80)
│   ├── /                     → web/dist/
│   └── /api/*                → control-plane:8080
├── control-plane  (Go)       → 主 API (:8080)
├── worker         (Go)       → Agent 执行 + 文档处理 + 邮件 + 搜索 (:8081)
├── collector      (Go)       → 可观测性数据收集 (:8082)
├── fetcher        (Go)       → URL 抓取 (:8083)
├── document-renderer          → 文档渲染 (:8084)
├── postgres                  → 主数据库 + 可观测性数据库 (:5432 + :5433)
├── minio                     → S3 兼容存储 (:9000)
├── qdrant                    → 向量数据库 (:6333)
├── bifrost                   → 模型网关 (:56666)
├── jaeger                    → OpenTelemetry 追踪 (:16686)
└── mailpit                   → 邮件捕获 (:1025)
```

## What we build

### New files

| File | Purpose |
|---|---|
| `infra/control-plane/Dockerfile` | Multi-stage Go build → Alpine |
| `infra/worker/Dockerfile` | Multi-stage Go build → Alpine |
| `infra/fetcher/Dockerfile` | Multi-stage Go build → Alpine |
| `infra/nginx/Dockerfile` | Nginx + web/dist SPA |
| `infra/nginx/nginx.conf` | Reverse proxy rules |
| `infra/compose/compose.prod.yaml` | Production compose definition |
| `.github/workflows/deploy.yml` | CI/CD: build → push ECR → deploy |

### What already exists

- `infra/collector/Dockerfile` — usable as-is
- `infra/document-renderer/Dockerfile` — usable as-is
- `web/dist/` — pre-built frontend

## Differences from dev compose

- Add nginx for SPA serving + API reverse proxy
- Remove `--profile collector` / health check dependencies that don't apply
- Build all Go services from source (not just collector + renderer)
- Jaeger retained (lightweight, useful for debugging)
- Mailpit retained (no real SMTP yet)
- API keys via EC2 `.env` file

## User responsibilities

1. Provision EC2 (t3.medium, Ubuntu 22.04) with Docker + Docker Compose
2. Create ECR repositories
3. Configure GitHub Actions secrets (AWS credentials, EC2 host, SSH key)
4. Point domain (when ready) to EC2 IP
5. Set API keys in `/home/ubuntu/nano-notebook/infra/compose/.env`

## Out of scope

- HTTPS / TLS (user can add later with Caddy/Nginx + Let's Encrypt)
- RDS / managed PostgreSQL
- S3 (MinIO is used instead)
- Backups
- Scaling / multi-instance
- OIDC (local credentials only)
- Real email (Mailpit capture)
