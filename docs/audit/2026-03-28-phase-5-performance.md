# Phase 5: Performance

Date: 2026-03-28
Branch: `audit/e2e-20260328`
Baseline commit: `b1e6088`

## What I profiled

I profiled 3 hot paths that show up on real request traffic or dashboard fanout:

1. **Proxy request preprocessing**
   - body parsing for request metadata
   - stream detection
   - model rewrites during routing / A-B selection
2. **Cache preprocessing**
   - cache eligibility
   - prompt normalization
   - model extraction
3. **WebSocket broadcast fanout**
   - one dashboard event pushed to 50 connected clients

## Biggest bottlenecks found

### 1. WebSocket fanout was fully serial

`ConnectionManager.broadcast()` awaited each `send_text()` one by one.

That means 50 slow clients turn into 50 sequential waits. It is the classic “one sleepy dashboard tab slows everyone else down” problem.

### 2. Cache preprocessing parsed the same JSON body over and over

`should_cache()`, `normalize_prompt()`, and `extract_model()` each parsed the request body separately.

That is pure CPU waste on every cacheable request.

### 3. Proxy metadata extraction and model rewrites re-parsed request JSON repeatedly

The proxy did separate `json.loads()` work for:

- request info extraction
- stream detection
- each model rewrite

The body itself already existed in memory. We were just making it work harder.

## Fixes made

### 1. Concurrent WebSocket broadcast

`src/tokenwatch/ws.py`

- switched broadcast fanout from serial sends to `asyncio.gather(..., return_exceptions=True)`
- still prunes dead connections after the broadcast completes

### 2. Shared request JSON parsing in cache helpers

`src/tokenwatch/cache.py`

- added a shared request JSON loader
- let `should_cache()`, `normalize_prompt()`, and `extract_model()` accept parsed data
- updated `cache_lookup()` and `cache_store_response()` to parse once and reuse it

### 3. Shared request JSON parsing in proxy helpers

`src/tokenwatch/proxy.py`

- added `_parse_json_body()`
- parse the request body once at the start of `_proxy_request()`
- reuse parsed data for:
  - request info extraction
  - stream detection
  - model rewrites
- avoid an extra `json.dumps()` when estimating token count from raw bytes

## Benchmarks

Benchmark script: `/tmp/tokenwatch_bench.py`

Measured against:

- **Before:** baseline commit `b1e6088`
- **After:** current working tree after performance changes

### Results

| Benchmark | Before | After | Improvement |
|---|---:|---:|---:|
| Proxy metadata pipeline | 29.82 µs | 26.85 µs | 9.96% faster |
| Cache preprocessing pipeline | 13.18 µs | 5.88 µs | 55.39% faster |
| WebSocket broadcast to 50 clients | 53.51 ms | 1.61 ms | 96.99% faster |

## Why these numbers matter

- **Proxy preprocessing** runs on every request.
- **Cache preprocessing** runs on every cacheable request and was burning CPU for no value.
- **WebSocket broadcast** is user-facing. The old serial path would get visibly sluggish as clients piled up.

## Verification

Functional verification after the perf changes:

```bash
python -m pytest -q
```

Result:

- **43 tests passed**
- **0 failures**
- **0 errors**

## Performance phase takeaway

The big win here was not exotic algorithm work. It was removing self-inflicted overhead:

- stop parsing the same JSON 3 times
- stop broadcasting to 50 clients like it is still 2004
