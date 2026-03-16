"""Semantic prompt caching for TokenWatch using Oracle AI Vector Search."""

import hashlib
import json
import logging

from .config import CACHE_SIMILARITY_THRESHOLD, CACHE_TTL
from .db import Database

logger = logging.getLogger("tokenwatch")


def normalize_prompt(body: bytes, api_type: str) -> str:
    """Extract and normalize semantic content from request body."""
    try:
        data = json.loads(body)
    except (json.JSONDecodeError, ValueError):
        return ""

    parts = []
    if api_type == "anthropic":
        if "system" in data:
            sys = data["system"]
            if isinstance(sys, str):
                parts.append(sys)
            elif isinstance(sys, list):
                for block in sys:
                    if isinstance(block, dict) and block.get("type") == "text":
                        parts.append(block["text"])
        for msg in data.get("messages", []):
            content = msg.get("content", "")
            if isinstance(content, str):
                parts.append(content)
            elif isinstance(content, list):
                for block in content:
                    if isinstance(block, dict) and block.get("type") == "text":
                        parts.append(block.get("text", ""))
    else:  # openai
        for msg in data.get("messages", []):
            content = msg.get("content", "")
            if isinstance(content, str):
                parts.append(content)

    normalized = " ".join(parts).strip().lower()
    return normalized


def hash_prompt(normalized: str) -> str:
    """SHA-256 hash of normalized prompt text."""
    return hashlib.sha256(normalized.encode()).hexdigest()


def should_cache(body: bytes) -> bool:
    """Determine if this request should use cache (skip if temperature > 0)."""
    try:
        data = json.loads(body)
        temp = data.get("temperature", 0)
        return temp == 0 or temp is None
    except (json.JSONDecodeError, ValueError):
        return False


def extract_model(body: bytes) -> str:
    """Extract model name from request body."""
    try:
        return json.loads(body).get("model", "")
    except (json.JSONDecodeError, ValueError):
        return ""


async def generate_embedding(db: Database, text: str) -> list[float] | None:
    """Generate embedding using Oracle DBMS_VECTOR_CHAIN.

    Uses the built-in ONNX model loaded into Oracle DB 26ai.
    Returns None if embedding generation fails.
    """
    if not text:
        return None
    try:
        async with db._pool.acquire() as conn:
            cursor = await conn.execute(
                """SELECT TO_VECTOR(DBMS_VECTOR_CHAIN.UTL_TO_EMBEDDING(:1,
                          JSON('{"provider":"database","model":"all_minilm_l12_v2"}')))
                   FROM dual""",
                [text[:8000]],
            )
            row = await cursor.fetchone()
            if row and row[0]:
                return row[0]
    except Exception:
        logger.exception("Failed to generate embedding")
    return None


async def cache_lookup(db: Database, body: bytes, api_type: str) -> dict | None:
    """Look up cached response for this prompt. Returns None on miss."""
    if not should_cache(body):
        return None

    normalized = normalize_prompt(body, api_type)
    if not normalized:
        return None

    prompt_hash = hash_prompt(normalized)
    model = extract_model(body)

    # Tier 1: exact match
    result = await db.cache_lookup_exact(prompt_hash, model)
    if result:
        logger.info("Cache HIT (exact) for model=%s", model)
        return result

    # Tier 2: semantic similarity
    embedding = await generate_embedding(db, normalized)
    if embedding:
        result = await db.cache_lookup_semantic(embedding, model, CACHE_SIMILARITY_THRESHOLD)
        if result:
            logger.info("Cache HIT (semantic, similarity=%.3f) for model=%s", result["similarity"], model)
            return result

    return None


async def cache_store_response(db: Database, body: bytes, api_type: str, response_body: str):
    """Store a response in the cache after a successful upstream call."""
    if not should_cache(body):
        return

    normalized = normalize_prompt(body, api_type)
    if not normalized:
        return

    prompt_hash = hash_prompt(normalized)
    model = extract_model(body)

    embedding = await generate_embedding(db, normalized)
    if embedding:
        await db.cache_store(prompt_hash, model, embedding, response_body, CACHE_TTL)
        logger.debug("Cached response for model=%s hash=%s", model, prompt_hash[:12])
