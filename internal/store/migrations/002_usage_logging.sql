CREATE TABLE usage_events (
    event_id UUID PRIMARY KEY,
    request_id TEXT NOT NULL,
    api_key_id UUID NOT NULL,
    client_id UUID NOT NULL,
    route TEXT NOT NULL CHECK (route LIKE '/%'),
    method TEXT NOT NULL,
    status_code SMALLINT NOT NULL CHECK (status_code BETWEEN 100 AND 599),
    duration_microseconds BIGINT NOT NULL CHECK (duration_microseconds >= 0),
    response_bytes BIGINT NOT NULL CHECK (response_bytes >= 0),
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX usage_events_client_time_idx ON usage_events(client_id, occurred_at DESC);

CREATE TABLE usage_hourly (
    bucket_start TIMESTAMPTZ NOT NULL,
    client_id UUID NOT NULL,
    route TEXT NOT NULL,
    status_code SMALLINT NOT NULL,
    request_count BIGINT NOT NULL CHECK (request_count >= 0),
    total_duration_microseconds BIGINT NOT NULL CHECK (total_duration_microseconds >= 0),
    total_response_bytes BIGINT NOT NULL CHECK (total_response_bytes >= 0),
    rate_limited_count BIGINT NOT NULL CHECK (rate_limited_count >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket_start, client_id, route, status_code)
);

CREATE TABLE usage_dead_letters (
    event_id UUID PRIMARY KEY,
    payload JSONB NOT NULL,
    last_error TEXT NOT NULL,
    attempt_count INTEGER NOT NULL CHECK (attempt_count > 0),
    failed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
