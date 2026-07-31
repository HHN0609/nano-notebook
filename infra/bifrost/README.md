# Bifrost config

`config.json` is the file-based Bifrost configuration described in
[ADR 0010](../../docs/technical-architecture/adr/0010-use-a-standalone-bifrost-model-gateway.md).
It is gitignored and **must exist on disk before `docker compose up` starts
the `bifrost` service** (mounted at `/app/data` — see
`infra/compose/compose.prod.yaml` / `compose.yaml`). Start from
`config.example.json`.

If `config.json` is missing, Bifrost silently falls back to its own
DB-backed config store with zero providers registered. Chat and embedding
calls then fail (`could not auto resolve a provider` / `failed to get
config for provider ... not found`) with no error until something actually
tries to use a model.

## Local dev vs. production: different DashScope endpoint

`providers.aliyun.network_config.base_url` **must differ by environment**
— this is exactly why the file is gitignored instead of committed:

| Environment | `base_url` | Why |
|---|---|---|
| Local dev | `https://dashscope.aliyuncs.com/compatible-mode` | Mainland China DashScope account/key |
| Production (AWS `ap-southeast-1`, Singapore) | `https://dashscope-intl.aliyuncs.com/compatible-mode` | Mainland endpoint is not reachable from outside China — requests hang until Bifrost's own retry/timeout budget is exhausted, not a clean error |

Using the wrong endpoint doesn't fail fast — the TCP connection just times
out, so chat requests hang for tens of seconds before failing. If chat
completions are timing out in prod, check this first.

`DASHSCOPE_API_KEY` (in `infra/compose/.env`) must be a key issued for
whichever region's endpoint you're pointing at.

## If the production server is ever rebuilt

`config.json` lives only on the EC2 instance's disk — it is not part of the
git-based deploy (`git reset --hard origin/main` in
`.github/workflows/deploy.yml` never touches gitignored files). A fresh
instance needs `infra/bifrost/config.json` recreated manually with the
`-intl` endpoint before Bifrost will route any model calls. See
`.pi/skills/nano-ssh/SKILL.md` for the current production instance details.
