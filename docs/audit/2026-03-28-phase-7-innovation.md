# Phase 7: Innovation

Date: 2026-03-28
Branch: `audit/e2e-20260328`

After the audit, the smartest additions were the ones that make the proxy easier to trust and easier to operate.

So I picked 3 features that improve real-world operator leverage instead of piling on shiny nonsense.

## Addition 1: Integrated auto-tagging

### Why it matters

The project already had cost attribution and tag-scoped budgets, but tagging still depended on callers remembering to send `X-TokenWatch-Tag`.

That is fragile.

Now the proxy can auto-tag requests from configured rules when the header is missing.

### What I added

- new module: `src/tokenwatch/tagging.py`
- new DB method: `Database.get_tag_rules()`
- proxy integration via `_resolve_feature_tag()`

### Rule format

It reuses `routing_rules` with these condition types:

- `tag_app`
- `tag_regex`

The first matching rule wins.

### Tests

- `tests/test_tagging.py`

Covers:

- app-based tagging
- regex-based tagging
- invalid regex handling
- explicit header winning over auto-tagging

## Addition 2: `tokenwatch explain-request`

### Why it matters

Operators need to answer:

- Will this request be cached?
- Which budget applies?
- Which routing rule fires?
- Which upstreams are candidates?
- Which model actually wins?

Before this feature, you had to infer that from code and config.

Now there is a dry-run explainer.

### What I added

- new module: `src/tokenwatch/explain.py`
- new CLI command: `tokenwatch explain-request`

It prints a decision trace without forwarding the request upstream.

### Output includes

- requested model
- final model
- resolved feature tag
- stream/cache eligibility
- budget allow/block state and warnings
- routing rule that matched
- A/B selection result
- upstream candidate order

### Tests

- `tests/test_explain.py`

Covers:

- helper-level decision trace generation
- CLI rendering path

## Addition 3: redacted prompt storage

### Why it matters

If prompt storage is enabled, storing raw request/response bodies is useful for replay, but it is also the fastest way to keep secrets and PII around longer than you meant to.

So I added an automatic redaction pass for stored payloads.

### What I added

- new config flag: `TOKENWATCH_REDACT_STORED_PROMPTS` (default `true`)
- new module: `src/tokenwatch/privacy.py`
- proxy integration before `db.store_prompt()`

### Redactions covered now

- secret-like JSON keys (`api_key`, `token`, `authorization`, `password`, `secret`)
- bearer tokens in free text
- `sk-*` / `rk-*` style tokens
- email addresses

### Tests

- `tests/test_privacy.py`

Covers:

- direct payload redaction
- end-to-end proxy prompt storage redaction

## Verification

```bash
python -m pytest -q
```

Result:

- **51 tests passed**
- **0 failures**
- **0 errors**

## Why these 3 won

They all improve the operator experience in a way the current product actually needs:

- **auto-tagging** makes cost attribution real instead of aspirational
- **explain-request** makes routing and budget behavior legible
- **redacted storage** makes replay safer to turn on

That is the kind of innovation that earns its keep.
