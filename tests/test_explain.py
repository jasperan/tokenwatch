"""Tests for request explanation and dry-run decision tracing."""

import json

import pytest
from click.testing import CliRunner

from tokenwatch.explain import build_request_explanation
from tokenwatch.models import ABTest, RoutingRule, Upstream


class ExplainDatabase:
    async def init(self):
        return None

    async def close(self):
        return None

    async def check_budget(self, source_app, model, feature_tag):
        return {
            "allowed": True,
            "warnings": [
                {
                    "budget_id": 1,
                    "scope": "tag",
                    "scope_value": feature_tag,
                    "limit": 10.0,
                    "spent": 8.5,
                    "period": "daily",
                    "action": "approaching",
                    "webhook_url": "",
                }
            ],
            "blocking_budget": None,
        }

    async def get_tag_rules(self):
        return [{"condition_type": "tag_regex", "condition_value": "invoice", "tag": "finance"}]

    async def get_routing_rules(self):
        return [
            RoutingRule(
                id=4,
                rule_name="cheap-open-webui",
                priority=10,
                condition_type="source_app",
                condition_value="open-webui",
                target_model="claude-haiku-4-5",
            )
        ]

    async def get_active_ab_tests(self):
        return [ABTest(id=9, test_name="latency", model_a="claude-haiku-4-5", model_b="claude-sonnet-4-5")]

    async def get_upstreams(self, api_type=None):
        return [
            Upstream(id=1, api_type="anthropic", base_url="https://primary.example", priority=10, is_healthy=True),
            Upstream(id=2, api_type="anthropic", base_url="https://secondary.example", priority=20, is_healthy=False),
        ]


@pytest.mark.asyncio
async def test_build_request_explanation_returns_decision_trace():
    db = ExplainDatabase()
    body = json.dumps(
        {
            "model": "claude-sonnet-4-5",
            "messages": [{"role": "user", "content": "please review this invoice"}],
            "temperature": 0,
        }
    ).encode()

    explanation = await build_request_explanation(
        db,
        api_type="anthropic",
        body=body,
        source_app="open-webui/1.0",
    )

    assert explanation["feature_tag"] == "finance"
    assert explanation["requested_model"] == "claude-sonnet-4-5"
    assert explanation["final_model"] == "claude-haiku-4-5"
    assert explanation["cache_eligible"] is True
    assert explanation["budget"]["allowed"] is True
    assert explanation["routing"]["applied"] is True
    assert explanation["routing"]["rule_id"] == 4
    assert explanation["ab_test"]["applied"] is False
    assert explanation["upstream_candidates"] == ["https://primary.example", "https://secondary.example"]


def test_explain_request_cli_prints_json(monkeypatch, tmp_path):
    from tokenwatch import cli as cli_module

    body_file = tmp_path / "request.json"
    body_file.write_text(json.dumps({"model": "claude-sonnet-4-5", "messages": [{"role": "user", "content": "invoice"}]}))

    monkeypatch.setattr(cli_module, "Database", ExplainDatabase)

    result = CliRunner().invoke(
        cli_module.cli,
        [
            "explain-request",
            "--api-type",
            "anthropic",
            "--body-file",
            str(body_file),
            "--source-app",
            "open-webui/1.0",
        ],
    )

    assert result.exit_code == 0
    rendered = result.stdout
    assert '"feature_tag": "finance"' in rendered
    assert '"final_model": "claude-haiku-4-5"' in rendered
