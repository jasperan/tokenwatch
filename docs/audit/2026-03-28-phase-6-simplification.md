# Phase 6: Ruthless Simplification

Date: 2026-03-28
Branch: `audit/e2e-20260328`

## What I cut

### 1. Deleted dead manual smoke scripts from the repo root

Removed:

- `test_anthropic.py`
- `test_direct.py`
- `test_comprehensive.py`

These were not real automated tests anymore.

They were stale, SQLite-era, network-dependent, and misleading enough to get auto-collected before phase 2 tightened pytest discovery.

Keeping them around would just preserve confusion.

### 2. Deleted the unused `tagger.py` module

Removed:

- `src/tokenwatch/tagger.py`

It had no callers in the runtime and no tests exercising it.

It was dead weight sitting next to production code, which is how ghost features survive for months.

### 3. Stripped unused imports from production modules

Cleaned up unused imports in:

- `src/tokenwatch/cli.py`
- `src/tokenwatch/interceptor.py`
- `src/tokenwatch/db.py`
- `src/tokenwatch/proxy.py`

Small change, but it matters. Fewer fake dependencies means less noise when reading the code.

## Why this phase matters

A repo gets weird long before it gets large.

Most of the weirdness here was not deep architecture. It was stale artifacts pretending to still matter:

- dead smoke tests
- a disconnected auto-tagging module
- unused imports hinting at logic that no longer exists

That kind of residue makes every future change harder because you waste time asking, “Do I need this?”

Now the answer is simpler more often.

## Verification

```bash
python -m pytest -q
```

Result:

- **43 tests passed**
- **0 failures**
- **0 errors**

## What I intentionally did not touch here

I left unrelated untracked local artifacts alone, including the placeholder docs and user-local files already sitting outside the committed repo state.

This phase was about simplifying committed project code, not vacuuming the operator’s workspace.
