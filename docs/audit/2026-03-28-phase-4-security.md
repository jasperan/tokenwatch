# Phase 4: Security Audit

Date: 2026-03-28
Branch: `audit/e2e-20260328`

## Audit scope

I checked for:

- insecure defaults
- hardcoded or weak secrets
- exposed services by default
- privacy footguns in prompt storage/caching
- dependency risk signals
- known dependency vulnerabilities

## Findings fixed now

### 1. Weak Oracle credentials were baked into runtime defaults

Before this phase:

- `TOKENWATCH_ORACLE_USER` defaulted to `tokenwatch`
- `TOKENWATCH_ORACLE_PASSWORD` defaulted to `tokenwatch`

That is a classic fail-open default. If an operator forgot to configure credentials, the app still tried to boot with weak, predictable values.

**Fix:**

- removed the hardcoded default user/password
- `Database.init()` now fails closed with a clear error if credentials are missing

### 2. The proxy and dashboard bound to `0.0.0.0` by default

That exposed both services to the whole network unless an operator remembered to lock them down some other way.

**Fix:**

- introduced `TOKENWATCH_HOST`
- default bind host is now `127.0.0.1`
- CLI `start` now takes `--host` and uses it for both the proxy and dashboard

### 3. Semantic caching was on by default

Semantic caching stores prompt-derived vectors and response bodies. That is useful, but it is also a privacy-sensitive feature.

**Fix:**

- `TOKENWATCH_CACHE_ENABLED` now defaults to `false`

That makes prompt retention-like behavior opt-in instead of quietly on.

## Findings flagged for discussion

### 1. No built-in auth for remote exposure

Binding to localhost by default fixes the insecure default.

But if someone intentionally runs `--host 0.0.0.0`, the proxy and dashboard still rely on network perimeter controls rather than app-layer auth.

I did not bolt on an auth layer in this phase because that changes the deployment model. If remote exposure is a real target use case, I’d add one of these next:

- shared secret header for both proxy and dashboard
- reverse proxy auth in front of both services
- dashboard-only auth at minimum

### 2. Docs still describe insecure or stale setup details

I found stale config/docs that still need cleanup later:

- `README.md` still describes weak Oracle defaults and cache enabled by default
- `.env.example` still references the old SQLite-era `TOKENWATCH_DB_PATH`

That gets fixed in phase 8 when I clone-test onboarding.

## Dependency audit

I ran `pip-audit` against the project dependencies derived from `pyproject.toml`.

### Result

- **1 known vulnerability found**
- package: `pygments` (transitive via `rich`)
- advisory: `CVE-2026-4539` / `GHSA-5239-wwwm-4pmq`
- issue class: inefficient regular expression complexity in `AdlLexer`
- current status: **no fix version published yet** in the advisory feed

### What I could fix right now

Nothing cleanly fixable in-repo yet.

This is a transitive dependency with no published patched version in the audit feed. I’m flagging it instead of pretending a pin would solve it.

## Tests added to prove the fixes

### `tests/test_security.py`

Covers:

- database init fails without explicit Oracle credentials
- config defaults are fail-closed and localhost-bound
- CLI `start` binds both servers to `127.0.0.1` by default

## Final verification

Commands used:

```bash
python -m pytest -q
pip-audit -r /tmp/tokenwatch-deps.txt -f json
```

Result:

- **43 tests passed**
- security fixes verified by tests
- dependency audit completed

## Security posture after phase 4

Better now:

- no more baked-in weak DB credentials
- no more network-wide bind by default
- no more prompt caching by default

Still worth discussing:

- auth model for intentional remote deployments
- how aggressively to document and monitor the transitive `pygments` advisory
