"""Explain how TokenWatch will handle a request before it is sent upstream."""

from .cache import should_cache
from .failover import get_upstream_candidates
from .proxy import _extract_request_info, _is_streaming, _parse_json_body, _resolve_feature_tag
from .router import evaluate_ab_test, evaluate_routing


async def build_request_explanation(
    db,
    api_type: str,
    body: bytes,
    source_app: str = "tokenwatch-explain",
    feature_tag: str = "",
) -> dict:
    """Return a decision trace for a request without forwarding it upstream."""
    parsed_body = _parse_json_body(body)
    body_or_data = parsed_body if parsed_body is not None else body
    request_info = _extract_request_info(body_or_data)
    requested_model = request_info["model"]
    is_streaming = _is_streaming(body_or_data)
    resolved_tag = await _resolve_feature_tag(db, feature_tag, source_app, request_info["first_message"])

    budget_result = await db.check_budget(source_app, requested_model, resolved_tag)
    routing_decision = await evaluate_routing(
        db,
        requested_model,
        source_app,
        request_info["first_message"],
        request_info["est_tokens"],
        0.0,
    )

    routed_model = routing_decision.model if routing_decision.was_rerouted else requested_model
    ab_model = routed_model
    ab_test_id = None
    if not routing_decision.was_rerouted:
        ab_model, ab_test_id = await evaluate_ab_test(db, "explain-request", routed_model, source_app)

    upstream_candidates = await get_upstream_candidates(
        db,
        api_type,
        routing_decision.upstream if routing_decision.was_rerouted else "",
    )

    return {
        "api_type": api_type,
        "source_app": source_app,
        "feature_tag": resolved_tag,
        "requested_model": requested_model,
        "final_model": ab_model,
        "is_streaming": is_streaming,
        "cache_eligible": (not is_streaming) and should_cache(body_or_data),
        "estimated_input_tokens": request_info["est_tokens"],
        "budget": {
            "allowed": budget_result["allowed"],
            "warnings": budget_result["warnings"],
            "blocking_budget": budget_result["blocking_budget"],
        },
        "routing": {
            "applied": routing_decision.was_rerouted,
            "rule_id": routing_decision.rule_id,
            "rule_name": routing_decision.rule_name,
            "target_model": routing_decision.model if routing_decision.was_rerouted else None,
            "override_upstream": routing_decision.upstream or None,
        },
        "ab_test": {
            "applied": ab_test_id is not None,
            "test_id": ab_test_id,
            "selected_model": ab_model if ab_test_id is not None else None,
        },
        "upstream_candidates": upstream_candidates,
    }
