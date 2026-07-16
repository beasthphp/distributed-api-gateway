package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Registry is intentionally small and dependency-free. It exposes metrics in
// Prometheus text format while keeping labels bounded to HTTP status codes.
type Registry struct {
	requests                atomic.Uint64
	inFlight                atomic.Int64
	durationNano            atomic.Uint64
	usageQueueCapacity      atomic.Int64
	usageQueueDepth         atomic.Int64
	usageEnqueued           atomic.Uint64
	usageDropped            atomic.Uint64
	usageRetries            atomic.Uint64
	usageBatches            atomic.Uint64
	usagePersisted          atomic.Uint64
	usageDeadLettered       atomic.Uint64
	usageDeadLetterFailures atomic.Uint64
	mu                      sync.RWMutex
	responses               map[int]uint64
}

func (r *Registry) SetUsageQueueCapacity(capacity int) {
	r.usageQueueCapacity.Store(int64(capacity))
}

func (r *Registry) SetUsageQueueDepth(depth int) {
	r.usageQueueDepth.Store(int64(depth))
}

func (r *Registry) RecordUsageEnqueued() {
	r.usageEnqueued.Add(1)
}

func (r *Registry) RecordUsageDropped(count int) {
	if count > 0 {
		r.usageDropped.Add(uint64(count))
	}
}

func (r *Registry) RecordUsageRetry() {
	r.usageRetries.Add(1)
}

func (r *Registry) RecordUsageBatch(events int) {
	r.usageBatches.Add(1)
	if events > 0 {
		r.usagePersisted.Add(uint64(events))
	}
}

func (r *Registry) RecordUsageDeadLettered(count int) {
	if count > 0 {
		r.usageDeadLettered.Add(uint64(count))
	}
}

func (r *Registry) RecordUsageDeadLetterFailure(count int) {
	if count > 0 {
		r.usageDeadLetterFailures.Add(uint64(count))
	}
}

func NewRegistry() *Registry {
	return &Registry{responses: make(map[int]uint64)}
}

func (r *Registry) Begin() func(int, time.Duration) {
	r.requests.Add(1)
	r.inFlight.Add(1)
	return func(status int, duration time.Duration) {
		r.inFlight.Add(-1)
		r.durationNano.Add(uint64(duration))
		r.mu.Lock()
		r.responses[status]++
		r.mu.Unlock()
	}
}

func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	r.mu.RLock()
	codes := make([]int, 0, len(r.responses))
	for code := range r.responses {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	responses := make(map[int]uint64, len(r.responses))
	for _, code := range codes {
		responses[code] = r.responses[code]
	}
	r.mu.RUnlock()

	fmt.Fprintln(w, "# HELP gateway_http_requests_total Total requests handled by protected API routes.")
	fmt.Fprintln(w, "# TYPE gateway_http_requests_total counter")
	fmt.Fprintf(w, "gateway_http_requests_total %d\n", r.requests.Load())
	fmt.Fprintln(w, "# HELP gateway_http_requests_in_flight Requests currently being handled.")
	fmt.Fprintln(w, "# TYPE gateway_http_requests_in_flight gauge")
	fmt.Fprintf(w, "gateway_http_requests_in_flight %d\n", r.inFlight.Load())
	fmt.Fprintln(w, "# HELP gateway_http_request_duration_seconds_sum Total request duration in seconds.")
	fmt.Fprintln(w, "# TYPE gateway_http_request_duration_seconds_sum counter")
	fmt.Fprintf(w, "gateway_http_request_duration_seconds_sum %.6f\n", float64(r.durationNano.Load())/float64(time.Second))
	fmt.Fprintln(w, "# HELP gateway_http_responses_total Responses grouped by HTTP status code.")
	fmt.Fprintln(w, "# TYPE gateway_http_responses_total counter")
	for _, code := range codes {
		fmt.Fprintf(w, "gateway_http_responses_total{code=%q} %d\n", fmt.Sprint(code), responses[code])
	}
	fmt.Fprintln(w, "# HELP gateway_usage_queue_capacity Maximum number of usage events that can wait in memory.")
	fmt.Fprintln(w, "# TYPE gateway_usage_queue_capacity gauge")
	fmt.Fprintf(w, "gateway_usage_queue_capacity %d\n", r.usageQueueCapacity.Load())
	fmt.Fprintln(w, "# HELP gateway_usage_queue_depth Usage events currently waiting in the bounded queue.")
	fmt.Fprintln(w, "# TYPE gateway_usage_queue_depth gauge")
	fmt.Fprintf(w, "gateway_usage_queue_depth %d\n", r.usageQueueDepth.Load())
	fmt.Fprintln(w, "# HELP gateway_usage_events_enqueued_total Usage events accepted by the queue.")
	fmt.Fprintln(w, "# TYPE gateway_usage_events_enqueued_total counter")
	fmt.Fprintf(w, "gateway_usage_events_enqueued_total %d\n", r.usageEnqueued.Load())
	fmt.Fprintln(w, "# HELP gateway_usage_events_dropped_total Usage events lost to validation, backpressure, shutdown, or dead-letter failure.")
	fmt.Fprintln(w, "# TYPE gateway_usage_events_dropped_total counter")
	fmt.Fprintf(w, "gateway_usage_events_dropped_total %d\n", r.usageDropped.Load())
	fmt.Fprintln(w, "# HELP gateway_usage_retries_total Usage batch retry delays entered after storage failures.")
	fmt.Fprintln(w, "# TYPE gateway_usage_retries_total counter")
	fmt.Fprintf(w, "gateway_usage_retries_total %d\n", r.usageRetries.Load())
	fmt.Fprintln(w, "# HELP gateway_usage_batches_persisted_total Usage batches persisted successfully.")
	fmt.Fprintln(w, "# TYPE gateway_usage_batches_persisted_total counter")
	fmt.Fprintf(w, "gateway_usage_batches_persisted_total %d\n", r.usageBatches.Load())
	fmt.Fprintln(w, "# HELP gateway_usage_events_persisted_total Usage events acknowledged by idempotent batch storage.")
	fmt.Fprintln(w, "# TYPE gateway_usage_events_persisted_total counter")
	fmt.Fprintf(w, "gateway_usage_events_persisted_total %d\n", r.usagePersisted.Load())
	fmt.Fprintln(w, "# HELP gateway_usage_events_dead_lettered_total Usage events stored after exhausting normal retries.")
	fmt.Fprintln(w, "# TYPE gateway_usage_events_dead_lettered_total counter")
	fmt.Fprintf(w, "gateway_usage_events_dead_lettered_total %d\n", r.usageDeadLettered.Load())
	fmt.Fprintln(w, "# HELP gateway_usage_dead_letter_failures_total Usage events that could not be written to dead-letter storage.")
	fmt.Fprintln(w, "# TYPE gateway_usage_dead_letter_failures_total counter")
	fmt.Fprintf(w, "gateway_usage_dead_letter_failures_total %d\n", r.usageDeadLetterFailures.Load())
}
