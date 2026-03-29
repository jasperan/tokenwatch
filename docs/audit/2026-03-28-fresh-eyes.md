# Fresh Eyes Audit Findings

Date: 2026-03-28
Repo: `tokenwatch`
Branch: `audit/e2e-20260328`

## Scope

This is the phase 1 read-through only. I read the main runtime, CLI, DB layer, tests, README, and existing docs before changing behavior.

## Pre-existing repo state

The working tree was already dirty when I started. I left those unrelated changes alone and will avoid staging them unless they become part of the audit work:

- modified: `.gitignore`
- untracked: `.pi/`, `.pre-commit-config.yaml`, `TEST_RESULTS.md`, `docs/*`, `tokenwatch-dashboard.png`

## Findings

### 1. The docs and the code are fighting each other

The repo reads like a half-finished Oracle rewrite.

- `README.md` sells an Oracle-backed v2 product with caching, routing, budgets, A/B tests, replay, failover, and a live dashboard.
- `CLAUDE.md`, top-level manual test scripts, and `tests/test_proxy.py` still describe the old SQLite shape.
- Placeholder docs in `docs/` are checked in empty.
- `TEST_RESULTS.md` claims SQLite integration still works.

This makes the project feel confused fast.

### 2. The real automated test suite is tiny, and part of it is obviously stale

The current pytest suite mostly covers:

- response parsing in `tests/test_interceptor.py`
- `/health` in `tests/test_proxy.py`

That proxy test still mutates `db.db_path`, which no longer exists in the Oracle `Database` class. So even before running tests, the suite looked brittle and outdated.

### 3. Budget enforcement looks broken on paper

`src/tokenwatch/budget.py` and `src/tokenwatch/db.py` disagree on the shape of budget results.

Examples:

- `check_budget_gate()` expects keys like `limit`, `spent`, `action`, `scope`, `period`, `webhook_url`
- `Database.check_budget()` returns warnings with only `budget_id`, `spend`, `limit`
- `blocking_budget` is returned from `BudgetRecord.model_dump()`, which uses `limit_amount` and `action_on_limit`, not `limit` and `action`

That means the warning path and probably the block path can crash right where they should be protecting requests.

### 4. Scoped budgets probably ignore the current request

`Database.check_budget()` loops over all active budgets, but `_get_period_spend()` only uses the budget record itself, not the incoming `source_app`, `model`, or `feature_tag` to decide applicability.

That means an app-scoped or tag-scoped budget can affect unrelated traffic if the spend query happens to be non-zero.

### 5. “Failover” currently looks like “pick one upstream and hope”

`src/tokenwatch/failover.py` chooses a single upstream URL. In `proxy.py`, connect and timeout errors mark that upstream unhealthy and immediately return `502` or `504`.

That is health tracking, not actual request failover.

### 6. Routing and A/B testing look shallower than the README promises

A few examples:

- routing gets `daily_cost=0.0` every time in `proxy.py`
- A/B assignment is global, first-active-test wins, with no request scoping
- dashboard and CLI reporting for A/B tests do not appear to match the DB result shape
- `tagger.py` exists, but the main request path does not appear to use it

So the marketing surface is ahead of the runtime.

### 7. The dashboard frontend and backend are out of sync

The HTML dashboard expects fields that the API does not return consistently.

Examples I found while reading:

- recent requests table expects `model`, but `/api/recent` returns `model_used`
- cache stats UI expects `total_entries`, API returns `entries`
- cost-by-tag UI expects `feature_tag`, API returns `tag`
- cost-by-app UI expects `source_app`, API returns `app`
- live WebSocket updates emit `cost` and `timestamp`, but the table renderer expects `estimated_cost` and `created_at`

This is the kind of thing that makes a dashboard look haunted.

### 8. There are placeholders and dead-looking surfaces in production paths

A few examples:

- replay panel in `src/tokenwatch/dashboard/index.html` is still “Coming soon”
- placeholder docs in `docs/` are committed as empty shells
- `cli.py` has an unfinished `ab report` rendering path
- `tagger.py` looks experimental and disconnected

Not fatal, but it makes the repo feel less finished than the README suggests.

### 9. The git hygiene is close, but not quite there

`.gitignore` already excludes several local-agent artifacts, but `.pi/` is still untracked in the working tree right now.

Given the repo preferences, that should be ignored by default.

## Fresh-eyes priority order

Before any ambitious additions, I’d sand down these in order:

1. Make the test suite real and runnable against the current Oracle architecture.
2. Fix the budget gate contract and request scoping.
3. Make failover actually retry healthy upstreams.
4. Reconcile dashboard payloads with the backend.
5. Clean out stale SQLite references from tests and docs.

## Phase 1 exit criteria

Fresh-eyes read-through is complete. Next step is phase 2: run the full suite, record the breakage, and fix it with coverage added around the real critical paths.
