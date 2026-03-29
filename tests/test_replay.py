"""Tests for replay dry-run behavior."""

import json

import pytest

from tokenwatch.replay import run_replay


class StubReplayDatabase:
    def __init__(self, prompts):
        self.prompts = prompts

    async def get_prompts_for_replay(self, source_model, from_date, to_date):
        return self.prompts


@pytest.mark.asyncio
async def test_run_replay_returns_error_when_no_prompts_match():
    db = StubReplayDatabase([])

    result = await run_replay(db, "claude-sonnet-4-5", "glm-4.7", "2026-03-01", "2026-03-02", dry_run=True)

    assert result == {"error": "No prompts found matching criteria", "count": 0}


@pytest.mark.asyncio
async def test_run_replay_dry_run_estimates_cost_without_network_calls():
    db = StubReplayDatabase(
        [
            {
                "request_id": "req-1",
                "request_body": json.dumps({"model": "claude-sonnet-4-5", "messages": [{"role": "user", "content": "hello"}]}),
            },
            {
                "request_id": "req-2",
                "request_body": {"model": "claude-sonnet-4-5", "messages": [{"role": "user", "content": "hello again"}]},
            },
        ]
    )

    result = await run_replay(db, "claude-sonnet-4-5", "glm-4.7", "2026-03-01", "2026-03-02", dry_run=True)

    assert result["dry_run"] is True
    assert result["prompt_count"] == 2
    assert result["target_model"] == "glm-4.7"
    assert result["estimated_cost"] > 0
