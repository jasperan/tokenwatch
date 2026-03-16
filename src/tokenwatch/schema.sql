CREATE TABLE requests (
    id NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id VARCHAR2(200),
    api_type VARCHAR2(20) NOT NULL,
    model_requested VARCHAR2(100) DEFAULT '',
    model_used VARCHAR2(100) DEFAULT '',
    input_tokens NUMBER DEFAULT 0,
    output_tokens NUMBER DEFAULT 0,
    cache_creation_tokens NUMBER DEFAULT 0,
    cache_read_tokens NUMBER DEFAULT 0,
    latency_ms NUMBER DEFAULT 0,
    status_code NUMBER DEFAULT 0,
    source_app VARCHAR2(200) DEFAULT '',
    session_id VARCHAR2(200) DEFAULT '',
    feature_tag VARCHAR2(200) DEFAULT '',
    estimated_cost NUMBER(12,6),
    cache_hit NUMBER(1) DEFAULT 0,
    ab_test_id NUMBER,
    routing_rule_id NUMBER,
    created_at TIMESTAMP DEFAULT SYSTIMESTAMP
)
PARTITION BY RANGE (created_at) INTERVAL (NUMTODSINTERVAL(1, 'DAY'))
(PARTITION p_init VALUES LESS THAN (TIMESTAMP '2026-03-17 00:00:00'))

CREATE TABLE prompt_store (
    id NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id VARCHAR2(200) NOT NULL,
    request_body CLOB CHECK (request_body IS JSON),
    response_body CLOB CHECK (response_body IS JSON),
    prompt_hash VARCHAR2(64),
    created_at TIMESTAMP DEFAULT SYSTIMESTAMP
)

CREATE TABLE prompt_vectors (
    id NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    prompt_hash VARCHAR2(64) NOT NULL,
    model VARCHAR2(100),
    embedding VECTOR(1536, FLOAT32),
    response_body CLOB CHECK (response_body IS JSON),
    hit_count NUMBER DEFAULT 0,
    ttl_expires TIMESTAMP,
    created_at TIMESTAMP DEFAULT SYSTIMESTAMP
)

CREATE TABLE budgets (
    id NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope VARCHAR2(20) NOT NULL,
    scope_value VARCHAR2(200),
    limit_amount NUMBER(12,2) NOT NULL,
    period VARCHAR2(20) NOT NULL,
    action_on_limit VARCHAR2(20) DEFAULT 'block',
    webhook_url VARCHAR2(500),
    is_active NUMBER(1) DEFAULT 1,
    created_at TIMESTAMP DEFAULT SYSTIMESTAMP
)

CREATE TABLE routing_rules (
    id NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    rule_name VARCHAR2(200) NOT NULL,
    priority NUMBER DEFAULT 100,
    condition_type VARCHAR2(50) NOT NULL,
    condition_value VARCHAR2(500),
    target_model VARCHAR2(100) NOT NULL,
    target_upstream VARCHAR2(500),
    is_active NUMBER(1) DEFAULT 1,
    created_at TIMESTAMP DEFAULT SYSTIMESTAMP
)

CREATE TABLE ab_tests (
    id NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    test_name VARCHAR2(200) NOT NULL UNIQUE,
    model_a VARCHAR2(100) NOT NULL,
    model_b VARCHAR2(100) NOT NULL,
    split_pct NUMBER DEFAULT 50,
    status VARCHAR2(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT SYSTIMESTAMP
)

CREATE TABLE upstreams (
    id NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    api_type VARCHAR2(20) NOT NULL,
    base_url VARCHAR2(500) NOT NULL,
    priority NUMBER DEFAULT 100,
    is_healthy NUMBER(1) DEFAULT 1,
    last_check TIMESTAMP,
    fail_count NUMBER DEFAULT 0,
    created_at TIMESTAMP DEFAULT SYSTIMESTAMP
)

CREATE INDEX idx_requests_model_time ON requests(model_used, created_at) LOCAL

CREATE INDEX idx_requests_app_time ON requests(source_app, created_at) LOCAL

CREATE INDEX idx_requests_tag ON requests(feature_tag, created_at) LOCAL

CREATE INDEX idx_prompt_hash ON prompt_store(prompt_hash)

CREATE INDEX idx_vectors_hash ON prompt_vectors(prompt_hash)
