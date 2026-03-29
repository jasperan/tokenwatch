# Phase 9: Final Gate

Date: 2026-03-28
Branch: `audit/e2e-20260328`

## Final verification

```bash
python -m pytest -q
```

Result:

- **51 tests passed**
- **0 failures**
- **0 errors**

## Final summary

### What I found

- stale SQLite-era tests and docs still mixed into an Oracle rewrite
- broken budget gate contract
- scoped budgets that could affect unrelated traffic
- failover that did not really fail over
- dashboard/backend drift
- weak default DB credentials and network binding defaults
- repeated JSON parsing and serial WebSocket fanout wasting time
- dead manual smoke scripts and an unused tagging module

### What I fixed

- stabilized pytest collection and removed stale auto-collected smoke scripts
- added broad coverage around budgets, routing, failover, cache helpers, dashboard endpoints, WebSocket fanout, privacy, explainability, and tagging
- fixed budget result normalization and request-scope matching
- implemented real retry-based upstream failover for connect/timeouts
- fixed OpenAI streaming request IDs to use upstream IDs
- changed security defaults to fail closed:
  - no weak Oracle credential defaults
  - localhost binding by default
  - semantic cache off by default
- reduced CPU overhead in cache/proxy preprocessing
- made WebSocket broadcast concurrent
- removed dead code and stale repo artifacts
- fixed README and `.env.example` onboarding guidance

### What I added

- auto-tagging from `tag_app` / `tag_regex` rules
- `tokenwatch explain-request`
- redacted stored prompt/response payloads

### What is left to discuss

1. **Remote exposure auth**
   - Localhost-by-default is much safer.
   - If users will expose TokenWatch remotely, it still needs a proper auth story.

2. **Oracle bootstrap ergonomics**
   - The docs are better now.
   - A first-run helper for creating the app user would make onboarding less fiddly.

3. **Transitive dependency advisory**
   - `pip-audit` still reports `pygments` `CVE-2026-4539` with no published fix version yet.
   - This one is tracked, not fixable in-repo right now.

## Workspace note

The git working tree still contains unrelated pre-existing local files outside this audit branch work:

- `.gitignore` modified
- `.pi/` untracked
- `.pre-commit-config.yaml` untracked
- `TEST_RESULTS.md` untracked
- placeholder docs in `docs/` untracked
- `tokenwatch-dashboard.png` untracked

I intentionally left those alone unless they were part of the audited changes.
