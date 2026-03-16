"""Prompt replay engine for regression testing across models."""

import asyncio
import json
import logging

import httpx

from .config import PROXY_PORT

logger = logging.getLogger("tokenwatch")


async def run_replay(
    db,
    source_model: str,
    target_model: str,
    from_date: str,
    to_date: str,
    concurrency: int = 3,
    dry_run: bool = False,
) -> dict:
    """Replay stored prompts against a different model."""
    prompts = await db.get_prompts_for_replay(source_model, from_date, to_date)
    if not prompts:
        return {"error": "No prompts found matching criteria", "count": 0}

    if dry_run:
        from .config import MODEL_PRICING
        pricing = MODEL_PRICING.get(target_model)
        estimated = 0
        for p in prompts:
            body = json.loads(p["request_body"]) if isinstance(p["request_body"], str) else p["request_body"]
            chars = len(json.dumps(body))
            input_est = chars // 4
            output_est = 500
            if pricing:
                estimated += (input_est * pricing[0] + output_est * pricing[1]) / 1_000_000
        return {
            "prompt_count": len(prompts),
            "target_model": target_model,
            "estimated_cost": round(estimated, 4),
            "dry_run": True,
        }

    results = []
    semaphore = asyncio.Semaphore(concurrency)

    async def replay_one(prompt_data):
        async with semaphore:
            body = json.loads(prompt_data["request_body"]) if isinstance(prompt_data["request_body"], str) else prompt_data["request_body"]
            body["model"] = target_model
            body.pop("stream", None)

            api_type = "anthropic" if "system" in body else "openai"
            if api_type == "anthropic":
                proxy_url = f"http://localhost:{PROXY_PORT}/anthropic/v1/messages"
            else:
                proxy_url = f"http://localhost:{PROXY_PORT}/openai/v1/chat/completions"

            try:
                async with httpx.AsyncClient(timeout=120) as client:
                    resp = await client.post(
                        proxy_url,
                        json=body,
                        headers={
                            "Content-Type": "application/json",
                            "User-Agent": "tokenwatch-replay",
                            "X-TokenWatch-Tag": "replay",
                        },
                    )
                    return {
                        "request_id": prompt_data["request_id"],
                        "status_code": resp.status_code,
                        "response_length": len(resp.text),
                    }
            except Exception as e:
                return {
                    "request_id": prompt_data["request_id"],
                    "error": str(e),
                }

    tasks = [replay_one(p) for p in prompts]
    results = await asyncio.gather(*tasks)

    successes = sum(1 for r in results if "error" not in r and r.get("status_code", 0) < 400)
    errors = sum(1 for r in results if "error" in r or r.get("status_code", 0) >= 400)

    return {
        "prompt_count": len(prompts),
        "source_model": source_model,
        "target_model": target_model,
        "successes": successes,
        "errors": errors,
        "results": results,
        "dry_run": False,
    }
