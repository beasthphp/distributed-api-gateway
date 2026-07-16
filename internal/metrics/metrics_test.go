package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsageMetricsAreExposed(t *testing.T) {
	registry := NewRegistry()
	registry.SetUsageQueueCapacity(10)
	registry.SetUsageQueueDepth(3)
	registry.RecordUsageEnqueued()
	registry.RecordUsageDropped(2)
	registry.RecordUsageRetry()
	registry.RecordUsageBatch(4)
	registry.RecordUsageDeadLettered(1)
	registry.RecordUsageDeadLetterFailure(2)

	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"gateway_usage_queue_capacity 10",
		"gateway_usage_queue_depth 3",
		"gateway_usage_events_enqueued_total 1",
		"gateway_usage_events_dropped_total 2",
		"gateway_usage_retries_total 1",
		"gateway_usage_batches_persisted_total 1",
		"gateway_usage_events_persisted_total 4",
		"gateway_usage_events_dead_lettered_total 1",
		"gateway_usage_dead_letter_failures_total 2",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics body does not contain %q\n%s", expected, body)
		}
	}
}
