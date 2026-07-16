# Asynchronous usage logging

Phase 3 moves usage persistence off the synchronous request path. Only authenticated API requests produce usage events; health, metrics, and rejected authentication attempts are excluded because they have no client identity.

## Delivery model

1. Request instrumentation creates a random event UUID after the response outcome is known.
2. A non-blocking send offers the event to a bounded in-memory queue.
3. The worker flushes when the batch is full or the flush interval expires.
4. Failed batches retry with bounded exponential backoff.
5. PostgreSQL inserts event UUIDs with `ON CONFLICT DO NOTHING` and aggregates only newly inserted rows.
6. A batch that exhausts retries is written to `usage_dead_letters`.

This is at-least-once processing during normal retries with idempotent storage. It is not durable queueing: a process crash can lose events still held in memory. A broker or local write-ahead log would be a later reliability upgrade.

## Backpressure

The gateway never waits for queue capacity. When the queue is full, the usage event is dropped while the API response proceeds normally. This keeps latency and memory bounded, and increments `gateway_usage_events_dropped_total`.

The main operational signals are:

- `gateway_usage_queue_depth`
- `gateway_usage_events_enqueued_total`
- `gateway_usage_events_dropped_total`
- `gateway_usage_retries_total`
- `gateway_usage_batches_persisted_total`
- `gateway_usage_events_persisted_total`
- `gateway_usage_events_dead_lettered_total`
- `gateway_usage_dead_letter_failures_total`

Sustained queue depth, any drops, repeated retries, or dead letters should trigger investigation.

## PostgreSQL data

`usage_events` contains one row per accepted event. It stores normalized routes rather than raw resource paths and never stores API keys, headers, query strings, or bodies.

`usage_hourly` groups by hour, client, normalized route, and status code. The event insertion and aggregate update occur in one statement, so a transaction failure cannot leave them inconsistent.

Inspect recent aggregates:

```sql
SELECT bucket_start, client_id, route, status_code,
       request_count, total_duration_microseconds, rate_limited_count
FROM usage_hourly
ORDER BY bucket_start DESC, request_count DESC
LIMIT 50;
```

Inspect dead letters without printing their JSON payloads:

```sql
SELECT event_id, attempt_count, failed_at, last_error
FROM usage_dead_letters
ORDER BY failed_at DESC
LIMIT 50;
```

Dead letters are evidence for operator review, not an automatic replay API. Before replaying, determine whether the failure was transient or caused by invalid data.

## Shutdown behavior

Gateway shutdown first stops HTTP admission, then closes and drains the queue. The final partial batch is flushed within `USAGE_SHUTDOWN_TIMEOUT`. If that deadline expires, storage is cancelled and the affected events are counted as dropped.
