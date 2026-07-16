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
	requests     atomic.Uint64
	inFlight     atomic.Int64
	durationNano atomic.Uint64
	mu           sync.RWMutex
	responses    map[int]uint64
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
}
