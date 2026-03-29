"""Tests for automatic request tagging."""

import pytest

from tokenwatch.proxy import _resolve_feature_tag
from tokenwatch.tagging import auto_tag


class StubTagDatabase:
    def __init__(self, rules):
        self.rules = rules

    async def get_tag_rules(self):
        return self.rules



def test_auto_tag_matches_app_rules_before_regex_rules():
    rules = [
        {"condition_type": "tag_app", "condition_value": "open-webui", "tag": "ui"},
        {"condition_type": "tag_regex", "condition_value": "invoice", "tag": "finance"},
    ]

    assert auto_tag("open-webui/1.0", "show me the invoice", rules) == "ui"



def test_auto_tag_ignores_invalid_regex_rules():
    rules = [{"condition_type": "tag_regex", "condition_value": "(", "tag": "broken"}]

    assert auto_tag("open-webui/1.0", "show me the invoice", rules) == ""


@pytest.mark.asyncio
async def test_resolve_feature_tag_prefers_explicit_header_over_auto_tag_rules():
    db = StubTagDatabase(
        [{"condition_type": "tag_regex", "condition_value": "invoice", "tag": "finance"}]
    )

    tag = await _resolve_feature_tag(db, "manual-tag", "open-webui/1.0", "show me the invoice")

    assert tag == "manual-tag"


@pytest.mark.asyncio
async def test_resolve_feature_tag_uses_auto_tag_when_header_is_missing():
    db = StubTagDatabase(
        [{"condition_type": "tag_regex", "condition_value": "invoice", "tag": "finance"}]
    )

    tag = await _resolve_feature_tag(db, "", "open-webui/1.0", "show me the invoice")

    assert tag == "finance"
