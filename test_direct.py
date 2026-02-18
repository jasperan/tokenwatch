#!/usr/bin/env python3
"""Test direct Anthropic API call without proxy"""
import os
import asyncio
import httpx

API_KEY = os.getenv("ANTHROPIC_API_KEY")
DIRECT_BASE_URL = "https://api.anthropic.com"

async def test_direct_call():
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
            {"role": "user", "content": "Say 'hello from direct test'"}
        ]
    }

    print("Making direct Anthropic API call (no proxy)...")
    async with httpx.AsyncClient() as client:
        response = await client.post(
            f"{DIRECT_BASE_URL}/v1/messages",
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

async def main():
    print("=" * 60)
    print("Testing Direct Anthropic API (without proxy)")
    print("=" * 60)

    result = await test_direct_call()
    if result:
        print("\n✓ Direct call successful!")
    else:
        print("\n✗ Direct call failed - API key may be invalid")

if __name__ == "__main__":
    asyncio.run(main())
