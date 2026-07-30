# Nano Notebook

A source-grounded research workspace for individual researchers and deep learners. Collect trusted material in a bounded Notebook, ask multi-step questions across your Sources, and verify every important conclusion against original evidence — not model knowledge.

Nano Notebook focuses on rigorous evidence-grounded research, not general-purpose chat.

## Features

- **Multi-format Source ingestion** — upload PDF, DOCX, PPTX, TXT, Markdown, HTML, YouTube transcripts, audio, and images. Each Source is processed through a durable pipeline with typed Evidence Coverage.
- **Private, source-grounded Chat** — every answer is strictly grounded in your selected Notebook Sources. Inline Citations link key claims to their supporting evidence.
- **Durable Research Agent** — multi-step, read-only Agent runs that survive page reloads. Stop, retry, or navigate away without losing work.
- **Notebook sharing with roles** — invite Viewers and Editors with Enterprise-style permissions. Sources are shared; every Member's Chat history stays private.
- **Grounded answers with passage-level Citations** — factual claims and synthesized conclusions require inline Citations that open the original Source at the relevant passage.

## Tech stack

| Layer | Technology |
| --- | --- |
| Backend | Go, modular monolith with PostgreSQL, MinIO, Qdrant |
| Frontend | React 19, TypeScript, Vite, Tailwind CSS 4, shadcn/ui |
| Model gateway | Bifrost (OpenAI / Gemini / Qwen) |
| Observability | OpenTelemetry |

## Getting started

### Prerequisites

- Go 1.25+
- Node.js 22+
- Docker

### Setup

```bash
# Bootstrap dependencies (PostgreSQL, MinIO, Qdrant, Bifrost, etc.)
make bootstrap

# Run database migrations and seed data
make migrate
make seed

# Start the Control Plane, Workers, and web dev server
make start
```

Additional commands:

```bash
make stop       # Stop all services
make reset      # Reset to a clean state
make health     # Check service health
make test-go    # Run Go tests
make test-web   # Run frontend tests
```

## Project structure

```
cmd/            Application entry points (Control Plane, Workers, etc.)
internal/       Go modules — Agent, Chat, Source, Retrieval, Notebook, Studio, etc.
web/            React + TypeScript SPA
docs/           Product discovery, technical architecture, and sprint runbooks
config/         Configuration files
infra/          Infrastructure definitions
agents/         Agent prompt catalog
skills/         Agent skill definitions
templates/      Output templates
memory/         Agent memory templates
evals/          Evaluation cases and harness
scripts/        Bootstrap, migration, and operational scripts
```

## Documentation

- [Product requirements](docs/product-discovery/REQUIREMENTS.md)
- [Product discovery & decisions](docs/product-discovery/DISCOVERY.md)
- [Technical architecture](docs/technical-architecture/ARCHITECTURE.md)
- [Architecture glossary](docs/technical-architecture/CONTEXT.md)
- [Sprint runbooks](docs/implementation/)
- [Third-party notices](docs/third-party-notices.md)

## License

MIT © 2026 Xinyu Huang — see [LICENSE](LICENSE).
