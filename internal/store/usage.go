package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/beasthphp/distributed-api-gateway/internal/usage"
)

// PersistUsage inserts raw events and updates hourly aggregates in one
// statement. Only rows newly inserted by the event-id conflict guard
// contribute to aggregates, which makes a retried batch idempotent.
func (p *Postgres) PersistUsage(ctx context.Context, events []usage.Event) error {
	if len(events) == 0 {
		return nil
	}
	const fields = 10
	casts := [...]string{"::uuid", "::text", "::uuid", "::uuid", "::text", "::text", "::smallint", "::bigint", "::bigint", "::timestamptz"}
	values := make([]string, 0, len(events))
	arguments := make([]any, 0, len(events)*fields)
	for index, event := range events {
		if err := event.Validate(); err != nil {
			return fmt.Errorf("validate usage event: %w", err)
		}
		start := index*fields + 1
		placeholders := make([]string, fields)
		for offset := range fields {
			placeholders[offset] = fmt.Sprintf("$%d%s", start+offset, casts[offset])
		}
		values = append(values, "("+strings.Join(placeholders, ", ")+")")
		arguments = append(arguments,
			event.ID, event.RequestID, event.APIKeyID, event.ClientID,
			event.Route, event.Method, event.StatusCode, event.DurationMicros,
			event.ResponseBytes, event.OccurredAt,
		)
	}

	query := `
		WITH incoming (
			event_id, request_id, api_key_id, client_id, route, method,
			status_code, duration_microseconds, response_bytes, occurred_at
		) AS (VALUES ` + strings.Join(values, ",") + `),
		inserted AS (
			INSERT INTO usage_events (
				event_id, request_id, api_key_id, client_id, route, method,
				status_code, duration_microseconds, response_bytes, occurred_at
			)
			SELECT event_id, request_id, api_key_id, client_id,
				route, method, status_code, duration_microseconds,
				response_bytes, occurred_at
			FROM incoming
			ON CONFLICT (event_id) DO NOTHING
			RETURNING client_id, route, status_code, duration_microseconds,
				response_bytes, occurred_at
		)
		INSERT INTO usage_hourly (
			bucket_start, client_id, route, status_code, request_count,
			total_duration_microseconds, total_response_bytes, rate_limited_count
		)
		SELECT date_trunc('hour', occurred_at), client_id, route, status_code,
			count(*), sum(duration_microseconds)::bigint, sum(response_bytes)::bigint,
			count(*) FILTER (WHERE status_code = 429)
		FROM inserted
		GROUP BY date_trunc('hour', occurred_at), client_id, route, status_code
		ON CONFLICT (bucket_start, client_id, route, status_code) DO UPDATE SET
			request_count = usage_hourly.request_count + EXCLUDED.request_count,
			total_duration_microseconds = usage_hourly.total_duration_microseconds + EXCLUDED.total_duration_microseconds,
			total_response_bytes = usage_hourly.total_response_bytes + EXCLUDED.total_response_bytes,
			rate_limited_count = usage_hourly.rate_limited_count + EXCLUDED.rate_limited_count,
			updated_at = now()
	`
	if _, err := p.pool.Exec(ctx, query, arguments...); err != nil {
		return fmt.Errorf("persist usage batch: %w", err)
	}
	return nil
}

func (p *Postgres) PersistUsageDeadLetters(ctx context.Context, events []usage.Event, lastError string, attempts int) error {
	if len(events) == 0 {
		return nil
	}
	const fields = 4
	values := make([]string, 0, len(events))
	arguments := make([]any, 0, len(events)*fields)
	for index, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode dead-letter event: %w", err)
		}
		start := index*fields + 1
		values = append(values, fmt.Sprintf("($%d::uuid, $%d::jsonb, $%d, $%d)", start, start+1, start+2, start+3))
		arguments = append(arguments, event.ID, payload, lastError, attempts)
	}
	query := `
		INSERT INTO usage_dead_letters(event_id, payload, last_error, attempt_count)
		VALUES ` + strings.Join(values, ",") + `
		ON CONFLICT (event_id) DO UPDATE SET
			payload = EXCLUDED.payload,
			last_error = EXCLUDED.last_error,
			attempt_count = EXCLUDED.attempt_count,
			failed_at = now()
	`
	if _, err := p.pool.Exec(ctx, query, arguments...); err != nil {
		return fmt.Errorf("persist usage dead letters: %w", err)
	}
	return nil
}
