# Phase 3: Battle Hardening

Date: 2026-03-28
Branch: `audit/e2e-20260328`

## Goal

Try to break the proxy on purpose.

I focused on the places most likely to fail under real traffic:

- upstream connection drops
- upstream timeouts
- malformed request bodies
- empty upstream responses
- garbage streaming events
- concurrent WebSocket broadcasts

## What broke

### 1. Failover was mostly theoretical

Before this phase, the proxy selected one upstream and gave up immediately on `ConnectError` or `TimeoutException`.

That meant a configured secondary upstream was useless during the exact kind of outage it exists for.

### 2. OpenAI streaming records kept synthetic request IDs

`new_streaming_record()` starts with a generated UUID.

`parse_openai_sse_event()` only copied the upstream `id` if the record had no existing request ID, so OpenAI streams never replaced the synthetic UUID with the real upstream request ID.

That made streamed request logs less trustworthy than non-streaming logs.

## Fixes made

### 1. Real failover candidate ordering

`src/tokenwatch/failover.py` now exposes ordered upstream candidates instead of one winner.

Behavior now:

- healthy upstreams first
- unhealthy upstreams still tried after that
- override URL stays authoritative and is tried alone
- if everything is marked unhealthy, the proxy still tries all of them in priority order instead of shrugging and dying

### 2. Proxy retry loop for connect and timeout failures

`src/tokenwatch/proxy.py` now retries across candidate upstreams for both:

- non-streaming requests
- streaming request setup

If one upstream drops or times out, the proxy marks it unhealthy and moves to the next candidate.

Only after all candidates fail does it return:

- `502` for connection failure
- `504` for timeout

### 3. Better upstream failure response consistency

The proxy now builds connection/timeout error responses through a single helper instead of duplicating small branches.

### 4. OpenAI streaming request IDs are now trustworthy

`src/tokenwatch/interceptor.py` now lets OpenAI SSE events replace the synthetic request ID with the upstream-provided one.

## Tests added

### `tests/test_battle_hardening.py`

Covers:

- healthy/unhealthy upstream ordering
- non-streaming failover after a connection drop
- non-streaming timeout after all upstreams fail
- streaming failover plus garbage SSE events
- empty upstream responses with malformed request bodies

### `tests/test_ws.py`

Added a concurrency-oriented test to verify concurrent broadcasts still deliver both messages.

## Final verification

```bash
python -m coverage erase && python -m coverage run --source=src/tokenwatch -m pytest -q
python -m coverage report --include='src/tokenwatch/*'
```

Result:

- **40 tests passed**
- **0 failures**
- **0 errors**
- overall `src/tokenwatch/*` coverage: **39%**

## Battle-hardening notes

What I proved in this phase:

- the proxy now survives initial upstream connect failures and timeouts by trying the next candidate
- malformed request JSON does not blow up the non-streaming path
- empty bodies do not crash response parsing
- garbage SSE events do not crash stream completion or request logging
- concurrent WebSocket broadcasts keep both messages

What still deserves more abuse later:

- mid-stream upstream disconnects after headers are already sent
- Oracle DB outages during request logging
- cache embedding generation failures under load
- CLI workflows against real Oracle fixtures
