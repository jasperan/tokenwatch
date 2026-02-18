#!/usr/bin/env python3
"""Test script to verify tokenwatch proxy with Anthropic API calls"""
import os
import asyncio
import json
import httpx

API_KEY = os.getenv("ANTHROPIC_API_KEY")
PROXY_BASE_URL = "http://localhost:8877/anthropic"

async def test_anthropic_call():
    if not API_KEY:
        print("Error: ANTHROPIC_API_KEY not set")
        return

    headers = {
        "x-api-key": API_KEY,
        "anthropic-version": "2023-06-01",
        "content-type": "application/json"
    }

    payload = {
        "model": "claude-3-5-sonnet-20241022",
        "max_tokens": 1024,
        "messages": [
            {"role": "user", "content": "Say 'hello from tokenwatch test'"}
        ]
    }

    print("Making Anthropic API call through tokenwatch proxy...")
    async with httpx.AsyncClient() as client:
        response = await client.post(
            f"{PROXY_BASE_URL}/v1/messages",
            headers=headers,
            json=payload,
            timeout=30.0
        )

    print(f"Response status: {response.status_code}")

    if response.status_code == 200:
        data = response.json()
        print(f"Response: {data.get('content', [{}])[0].get('text', '')[:100]}")
        return data
    else:
        print(f"Error: {response.text}")
        return None

async def check_tokenwatch_db():
    import sqlite3
    conn = sqlite3.connect("/home/ubuntu/.tokenwatch/usage.db")
    cursor = conn.cursor()

    # Get recent requests
    cursor.execute("""
        SELECT id, api_type, model, input_tokens, output_tokens, estimated_cost, created_at
        FROM requests
        ORDER BY created_at DESC
        LIMIT 5
    """)
    rows = cursor.fetchall()

    print("\n--- Recent TokenWatch Requests ---")
    for row in rows:
        print(f"ID: {row[0]}")
        print(f"  API Type: {row[1]}")
        print(f"  Model: {row[2]}")
        print(f"  Input Tokens: {row[3]}")
        print(f"  Output Tokens: {row[4]}")
        print(f"  Cost: ${row[5]:.6f}")
        print(f"  Time: {row[6]}")
        print()

    conn.close()

async def main():
    print("=" * 60)
    print("Testing TokenWatch with Anthropic API")
    print("=" * 60)

    result = await test_anthropic_call()
    if result:
        print("\n✓ Test successful!")
        await check_tokenwatch_db()
    else:
        print("\n✗ Test failed")

if __name__ == "__main__":
    asyncio.run(main())
