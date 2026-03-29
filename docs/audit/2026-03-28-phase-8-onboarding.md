# Phase 8: Onboarding Verify

Date: 2026-03-28
Branch: `audit/e2e-20260328`

## What I did

I clone-tested the README flow from a fresh copy of the repo.

I verified:

1. create a fresh workspace copy
2. create a fresh virtualenv
3. run `pip install -e .`
4. start TokenWatch with Oracle env vars set
5. hit the proxy health endpoint
6. hit the dashboard stats endpoint
7. run the new `tokenwatch explain-request` command from the installed CLI

## Friction I hit

### 1. The README assumed Oracle was always on host port `1521`

That was false on this machine.

A container named `oracle-free` already existed, and its host mapping was:

```bash
docker port oracle-free 1521
# 0.0.0.0:1523
```

So the previous README would have sent new users straight into connection failures.

**Fix:** the README now tells users to check the mapped host port and use that in `TOKENWATCH_ORACLE_DSN` if needed.

### 2. The README still told users to rely on weak built-in DB credentials

That was out of date after the security phase.

**Fix:** the README now explains that TokenWatch has no insecure DB credential defaults, and it includes a concrete Oracle SQL snippet to create a dedicated `TOKENWATCH` user.

### 3. `.env.example` was still SQLite-era and incomplete

It still contained `TOKENWATCH_DB_PATH`, which no longer belongs in this project.

**Fix:** `.env.example` now reflects the current Oracle-based config surface, including:

- `TOKENWATCH_HOST`
- `TOKENWATCH_ORACLE_*`
- cache / replay flags
- redacted prompt storage flag
- OTEL settings

### 4. Dashboard docs were stale

The README still described the older tab layout.

**Fix:** the README now matches the actual UI:

- Overview
- Cost Intelligence
- Experiments
- System

### 5. The architecture section lagged behind the implementation

It was missing:

- auto-tagging behavior
- multi-candidate failover wording
- redacted prompt storage

**Fix:** updated the architecture walkthrough to match the current runtime.

## What worked after the doc fixes

Using a fresh working copy and virtualenv:

- `pip install -e .` succeeded
- `tokenwatch start` succeeded with:
  - `TOKENWATCH_ORACLE_DSN=localhost:1523/FREEPDB1`
  - `TOKENWATCH_ORACLE_USER=TOKENWATCH`
  - `TOKENWATCH_ORACLE_PASSWORD=TokenWatch123`
- `GET /health` returned `200`
- `GET /api/stats?timeframe=24h` returned `200`
- `tokenwatch explain-request ...` returned a valid JSON decision trace

## Verification notes

I terminated the verification process after confirming startup and basic endpoint health. That termination was intentional, not a crash.

## Onboarding result

The docs are now a lot less likely to send a new operator into:

- the wrong Oracle port
- missing DB user setup
- stale SQLite config
- stale dashboard expectations

Still left to discuss later:

- whether we want a helper script that creates the Oracle app user automatically
- whether Quick Start should include a minimal seed route/tag example so `explain-request` feels more concrete on first run
