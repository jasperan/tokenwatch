# Phase 2: Test Sweep

Date: 2026-03-28
Branch: `audit/e2e-20260328`

## What failed before any fixes

Running the suite as-is exposed 4 immediate problems:

1. `tests/test_proxy.py` still assumed a SQLite-style `db.db_path` test fixture.
2. `test_comprehensive.py` was being auto-collected by pytest even though it is a manual smoke script that expects:
   - real Oracle credentials
   - a running proxy on `localhost:8877`
   - a running dashboard on `localhost:8878`
3. pytest emitted an `asyncio_default_fixture_loop_scope` deprecation warning.
4. Critical production paths had almost no coverage, especially budgets, routing, failover, cache helpers, dashboard endpoints, WebSocket fanout, and replay dry runs.

## Fixes made

### 1. Tightened the automated suite boundary

`pytest` now collects from `tests/` only.

That keeps the automated suite focused on real unit/integration tests and stops accidental collection of ad hoc root-level smoke scripts.

### 2. Fixed the stale proxy test

`tests/test_proxy.py` no longer tries to initialize a fake SQLite database against Oracle-backed code.

It now tests the `/health` route directly, which is what the file was actually trying to verify.

### 3. Fixed the budget contract bug the tests uncovered

I added tests around `Database.check_budget()` and `check_budget_gate()`.

Those tests exposed a real production mismatch:

- `Database.check_budget()` returned one shape
- `budget.check_budget_gate()` expected another

I fixed the contract and added request-scope matching so app/model/tag budgets only affect the traffic they actually target.

### 4. Added coverage for critical paths

New test files:

- `tests/test_budget.py`
- `tests/test_cache.py`
- `tests/test_dashboard_app.py`
- `tests/test_failover.py`
- `tests/test_proxy_helpers.py`
- `tests/test_replay.py`
- `tests/test_router.py`
- `tests/test_ws.py`

These cover:

- budget enforcement and warning/block behavior
- cache prompt normalization and cache eligibility rules
- dashboard API response shapes
- upstream selection and health marking
- proxy request helper parsing and header filtering
- replay dry-run logic
- routing rule matching and A/B assignment
- WebSocket broadcast/prune behavior

### 5. Removed the pytest-asyncio warning

`pyproject.toml` now sets `asyncio_default_fixture_loop_scope = "function"`.

## Coverage delta

Coverage was measured against `src/tokenwatch/*` in both runs.

- **Before:** 18%
- **After:** 33%
- **Delta:** +15 points

## Current green state

Final phase-2 verification:

```bash
python -m coverage erase && python -m coverage run --source=src/tokenwatch -m pytest -q
python -m coverage report -m --include='src/tokenwatch/*'
```

Result:

- **34 tests passed**
- **0 failures**
- **0 errors**

## Untested or lightly tested areas left for later phases

Still thin enough to deserve more pressure in later phases:

- full proxy forwarding path (`_proxy_non_streaming`, `_proxy_streaming`)
- Oracle-backed DB query methods beyond the budget logic
- CLI command surface
- telemetry integration
- tagger integration

That is okay for now. Phase 3 is where I start trying to break those paths on purpose.
