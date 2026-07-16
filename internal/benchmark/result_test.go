package benchmark

import (
	"bytes"
	"testing"
	"time"
)

func TestSummarizeLatencyUsesNearestRankPercentiles(t *testing.T) {
	values := make([]int64, 100)
	for index := range values {
		values[index] = int64(index+1) * 1000
	}
	summary := SummarizeLatency(values)
	if summary.P50 != 50 || summary.P95 != 95 || summary.P99 != 99 {
		t.Fatalf("p50/p95/p99 = %.0f/%.0f/%.0f, want 50/95/99", summary.P50, summary.P95, summary.P99)
	}
	if summary.Minimum != 1 || summary.Maximum != 100 || summary.Mean != 50.5 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestNewResultCountsStatusesErrorsAndUpstreams(t *testing.T) {
	result := NewResult(Metadata{
		Tool: "test", Scenario: "single", Target: "http://example.test",
		Concurrency: 2, GatewayInstances: 1, StartedAt: time.Now(), Duration: time.Second,
	}, []Sample{
		{LatencyMicros: 1000, StatusCode: 200, Upstream: "one"},
		{LatencyMicros: 2000, StatusCode: 429, Upstream: "one"},
		{LatencyMicros: 3000, TransportError: true},
	})
	if result.ThroughputRPS != 3 || result.TransportErrors != 1 || result.HTTPErrors != 1 || result.RateLimited != 1 {
		t.Fatalf("result counters = %+v", result)
	}
	if result.StatusCounts["200"] != 1 || result.StatusCounts["429"] != 1 || result.UpstreamCounts["one"] != 2 {
		t.Fatalf("status/upstream counts = %+v/%+v", result.StatusCounts, result.UpstreamCounts)
	}

	var encoded bytes.Buffer
	if err := Encode(&encoded, result); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Requests != 3 || len(decoded.Samples) != 3 {
		t.Fatalf("decoded = %+v", decoded)
	}
}
