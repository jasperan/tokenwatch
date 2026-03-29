# TokenWatch

[![Python 3.10+](https://img.shields.io/badge/python-3.10+-blue.svg?style=for-the-badge)](https://www.python.org/downloads/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=for-the-badge)](LICENSE)
![Oracle DB](https://img.shields.io/badge/storage-Oracle_DB_26ai_Free-F80000.svg?style=for-the-badge&logo=oracle&logoColor=white)
![Proxy](https://img.shields.io/badge/type-AI_Proxy-orange?style=for-the-badge)

**AI token proxy powered by Oracle DB 26ai Free.** Sits between your apps and upstream APIs (Anthropic, OpenAI-compatible), tracking every token, caching semantically similar prompts, enforcing budgets, and routing requests to the right model.

## What Is TokenWatch

TokenWatch is a transparent HTTP proxy that intercepts AI API traffic. Point your apps at `localhost:8877` instead of the upstream API, and TokenWatch handles the rest.

Every request flows through a pipeline: budget check, semantic cache lookup, smart routing, upstream forwarding, and structured logging. All data lands in Oracle DB 26ai Free, which also powers vector similarity search for the prompt cache.

v2 bolts on Oracle AI Vector Search for semantic caching, a budget kill switch, model routing, A/B testing, prompt replay, and OpenTelemetry export. The original transparent proxy and cost tracking are still there, just running on a proper database now.

## Key Features

- **Semantic prompt caching** via Oracle AI Vector Search (cosine similarity matching, configurable threshold)
- **Budget enforcement with kill switch** (per-app, per-model, per-tag limits with hard/warn/notify actions)
- **Smart model routing** (route requests by app, tag, content pattern, or time-of-day)
- **A/B testing framework** (split traffic between models, compare cost/latency/quality)
- **Prompt replay and regression testing** (re-run stored prompts against different models)
- **Cost attribution and tagging** (tag requests by feature, team, or session for granular cost breakdowns)
- **Real-time WebSocket dashboard** (4-tab UI with live updates, no polling)
- **OpenTelemetry export** (push traces and metrics to any OTLP-compatible collector)
- **Multi-provider failover** (automatic retry across upstream endpoints with health tracking)

## Quick Start

### 1. Start Oracle DB 26ai Free

Pick a strong system password and reuse that value in the setup command below.

```bash
docker run -d --name oracle-free -p 1521:1521 -e ORACLE_PWD='YOUR_ORACLE_SYSTEM_PASSWORD' container-registry.oracle.com/database/free:latest  # pragma: allowlist secret
```

If you already have an `oracle-free` container running on a different host port, use that mapped port in the DSN instead of assuming `1521`:

```bash
docker port oracle-free 1521
# Example output: 0.0.0.0:1523
```

### 2. Create an application user for TokenWatch

TokenWatch does **not** ship with weak default DB credentials anymore. Replace the placeholder passwords below with your real values and create a dedicated user first:

```bash
docker exec oracle-free bash -lc "cat <<'SQL' | sqlplus -s system/YOUR_ORACLE_SYSTEM_PASSWORD@localhost:1521/FREEPDB1  # pragma: allowlist secret
whenever sqlerror exit failure
begin
  execute immediate 'create user TOKENWATCH identified by YOUR_TOKENWATCH_DB_PASSWORD'; -- pragma: allowlist secret
exception
  when others then
    if sqlcode != -1920 then raise; end if;
end;
/
alter user TOKENWATCH identified by YOUR_TOKENWATCH_DB_PASSWORD; -- pragma: allowlist secret
grant connect, resource to TOKENWATCH;
grant create session, create table, create view, create sequence, create procedure to TOKENWATCH;
exit
SQL"
```

If your container is mapped to another host port, keep the SQL command as-is inside the container, but export the host-side DSN with the mapped port.

### 3. Install and run TokenWatch

```bash
pip install -e .

export TOKENWATCH_ORACLE_DSN="localhost:1521/FREEPDB1"
export TOKENWATCH_ORACLE_USER="TOKENWATCH"
export TOKENWATCH_ORACLE_PASSWORD="YOUR_TOKENWATCH_DB_PASSWORD"  # pragma: allowlist secret

tokenwatch start
```

By default, TokenWatch binds to `127.0.0.1`. If you need another interface, pass `--host` or set `TOKENWATCH_HOST`.

Point your apps at the proxy:

- Anthropic: `http://127.0.0.1:8877/anthropic`
- OpenAI: `http://127.0.0.1:8877/openai`

Open `http://127.0.0.1:8878` for the dashboard.

## Configuration

All settings via environment variables or `.env` file:

| Variable | Default | Description |
|---|---|---|
| `TOKENWATCH_ORACLE_DSN` | `localhost:1521/FREEPDB1` | Oracle DB connection string |
| `TOKENWATCH_ORACLE_USER` | `""` | Oracle DB username. Required. No insecure default. |
| `TOKENWATCH_ORACLE_PASSWORD` | `""` | Oracle DB password. Required. No insecure default. |
| `TOKENWATCH_HOST` | `127.0.0.1` | Bind host for proxy and dashboard |
| `TOKENWATCH_PROXY_PORT` | `8877` | Proxy listen port |
| `TOKENWATCH_DASHBOARD_PORT` | `8878` | Dashboard listen port |
| `TOKENWATCH_ANTHROPIC_URL` | `https://api.anthropic.com` | Anthropic upstream URL |
| `TOKENWATCH_OPENAI_URL` | `https://api.z.ai` | OpenAI-compatible upstream URL |
| `TOKENWATCH_CONNECT_TIMEOUT` | `10` | Connection timeout (seconds) |
| `TOKENWATCH_OVERALL_TIMEOUT` | `300` | Overall request timeout (seconds) |
| `TOKENWATCH_CACHE_ENABLED` | `false` | Enable semantic prompt caching |
| `TOKENWATCH_CACHE_TTL` | `86400` | Cache entry TTL (seconds) |
| `TOKENWATCH_CACHE_SIMILARITY_THRESHOLD` | `0.05` | Vector distance threshold for cache hits (lower = stricter) |
| `TOKENWATCH_STORE_PROMPTS` | `false` | Store prompt and response bodies for replay |
| `TOKENWATCH_REDACT_STORED_PROMPTS` | `true` | Redact obvious secrets and emails before stored payloads are written |
| `TOKENWATCH_PROMPT_RETENTION_DAYS` | `30` | Days to retain stored prompts |
| `TOKENWATCH_BUDGET_ENABLED` | `true` | Enable budget enforcement |
| `TOKENWATCH_OTEL_ENABLED` | `false` | Enable OpenTelemetry export |
| `TOKENWATCH_OTEL_ENDPOINT` | `http://localhost:4317` | OTLP collector endpoint |
| `TOKENWATCH_OTEL_SERVICE_NAME` | `tokenwatch` | Service name for traces |

## CLI Commands

| Command | Description |
|---|---|
| `tokenwatch start` | Start proxy + dashboard servers |
| `tokenwatch explain-request` | Show how TokenWatch would route, tag, budget-check, and forward a request without sending it |
| `tokenwatch stats` | Show token usage statistics |
| `tokenwatch tail` | Show recent requests |
| `tokenwatch status` | Check if proxy is running |
| `tokenwatch reset` | Clear all usage data |
| `tokenwatch budget set` | Set a spending budget (per-app, per-model, per-tag) |
| `tokenwatch budget status` | Show all budgets and current spend |
| `tokenwatch budget remove` | Remove a budget |
| `tokenwatch cost by-tag` | Cost breakdown by feature tag |
| `tokenwatch cost by-app` | Cost breakdown by source application |
| `tokenwatch cost by-session` | Most expensive conversations |
| `tokenwatch cost forecast` | Project future spending from recent trends |
| `tokenwatch route add` | Add a smart routing rule |
| `tokenwatch route list` | Show all routing rules |
| `tokenwatch route enable/disable` | Toggle a routing rule |
| `tokenwatch cache stats` | Show cache hit rate and size |
| `tokenwatch cache clear` | Clear cached responses |
| `tokenwatch ab create` | Create an A/B test between two models |
| `tokenwatch ab list` | Show active A/B tests |
| `tokenwatch ab report` | Show A/B test comparison report |
| `tokenwatch ab pause/complete` | Pause or finish an A/B test |
| `tokenwatch replay` | Replay stored prompts against a different model |
| `tokenwatch upstream add` | Add an upstream provider endpoint |
| `tokenwatch upstream list` | Show all upstream endpoints |
| `tokenwatch upstream remove` | Remove an upstream endpoint |

## Dashboard

The web dashboard at `http://127.0.0.1:8878` has 4 tabs:

1. **Overview**: real-time token usage charts, request table, and live request feed.
2. **Cost Intelligence**: cost by tag, app, and session, plus a forecast panel.
3. **Experiments**: A/B test status and replay placeholder UI.
4. **System**: routing stats, cache stats, budgets, and upstream health.

The dashboard uses WebSocket live events plus a periodic refresh loop for summary panels.

## Architecture

Every request passes through the same pipeline:

1. **Budget gate**: checks matching budgets. If a blocking budget is over limit, the request gets a `429` before it leaves the proxy.
2. **Cache lookup**: if semantic caching is enabled and the request is cache-eligible, TokenWatch checks exact then semantic matches.
3. **Tag resolution**: explicit `X-TokenWatch-Tag` wins. Otherwise TokenWatch can auto-tag from configured `tag_app` and `tag_regex` rules.
4. **Smart router**: routing rules are evaluated in priority order and can switch the model or upstream.
5. **A/B splitter**: if no routing rule already changed the model, an active A/B test can choose the final model.
6. **Upstream forward**: the request goes to the selected provider. If one upstream connect attempt fails or times out, TokenWatch tries the next candidate.
7. **Log and store**: the response is parsed for token counts, cost is estimated, and everything is written to Oracle DB. If prompt storage is enabled, stored payloads are redacted by default before replay data is written.
8. **Broadcast**: a summary is pushed to all connected WebSocket clients for the live dashboard.

## Supported Models

| Model | Input ($/1M tokens) | Output ($/1M tokens) |
|---|---|---|
| Claude Opus 4.6 | $15.00 | $75.00 |
| Claude Sonnet 4.5 | $3.00 | $15.00 |
| Claude Haiku 4.5 | $0.80 | $4.00 |
| GLM-4.7 | $0.60 | $0.60 |
| GLM-4.7 Flash | $0.06 | $0.06 |

---

<div align="center">

[![GitHub](https://img.shields.io/badge/GitHub-jasperan-181717?style=for-the-badge&logo=github&logoColor=white)](https://github.com/jasperan)&nbsp;
[![LinkedIn](https://img.shields.io/badge/LinkedIn-jasperan-0077B5?style=for-the-badge&logo=linkedin&logoColor=white)](https://www.linkedin.com/in/jasperan/)

</div>
