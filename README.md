# TokenWatch

Real-time API token usage proxy for Anthropic and OpenAI-compatible endpoints.

TokenWatch sits between your AI tools and upstream APIs, transparently logging token usage, costs, and latency to a local SQLite database. It provides both a CLI and web dashboard for monitoring.

## Architecture

```
Apps (OpenClaw, Claude Code, scripts)
        |
        v
  TokenWatch Proxy (localhost:8877)
        |
   +---------+---------+
   |                   |
   v                   v
Anthropic API     Z.AI / OpenAI API
```

## Installation

```bash
pip install -e .
```

## Quick Start

```bash
# Start the proxy + dashboard
tokenwatch start

# Configure your apps to point to the proxy:
#   Anthropic: http://localhost:8877/anthropic
#   OpenAI:    http://localhost:8877/openai
```

## Usage

```bash
tokenwatch start              # Start proxy (8877) + dashboard (8878)
tokenwatch stats              # Show usage statistics
tokenwatch stats -t 7d        # Stats for last 7 days
tokenwatch tail               # Live tail of requests
tokenwatch status             # Check if proxy is running
tokenwatch reset              # Clear all data
```

## Configuration

Set via environment variables or `.env` file:

| Variable | Default | Description |
|---|---|---|
| `TOKENWATCH_PROXY_PORT` | 8877 | Proxy listen port |
| `TOKENWATCH_DASHBOARD_PORT` | 8878 | Web dashboard port |
| `TOKENWATCH_DB_PATH` | `~/.tokenwatch/usage.db` | SQLite database path |
| `TOKENWATCH_ANTHROPIC_URL` | `https://api.anthropic.com` | Anthropic upstream |
| `TOKENWATCH_OPENAI_URL` | `https://api.z.ai` | OpenAI-compatible upstream |

## Web Dashboard

Open `http://localhost:8878` for a real-time dashboard with:
- Token usage over time (line chart)
- Tokens by model (doughnut chart)
- Recent requests table with latency and cost
- Timeframe filtering (1h, 24h, 7d, 30d, all)

## Cost Tracking

Built-in pricing for:
- Claude Opus 4.6, Sonnet 4.5, Haiku 4.5
- GLM-4.7, GLM-4.7-Flash
