package benchmark

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"
)

const SchemaVersion = 1

type Sample struct {
	LatencyMicros         int64  `json:"latency_us"`
	StatusCode            int    `json:"status_code"`
	TransportError        bool   `json:"transport_error"`
	RateLimitHeadersValid bool   `json:"rate_limit_headers_valid"`
	Upstream              string `json:"upstream,omitempty"`
}

type LatencySummary struct {
	Minimum float64 `json:"min"`
	Mean    float64 `json:"mean"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
	Maximum float64 `json:"max"`
}

type Verification struct {
	Passed                     bool            `json:"passed"`
	Checks                     map[string]bool `json:"checks"`
	Accepted                   int             `json:"accepted"`
	Denied                     int             `json:"denied"`
	TheoreticalMaximumAccepted int             `json:"theoretical_maximum_accepted,omitempty"`
	ObservedUpstreams          int             `json:"observed_upstreams"`
}

type Result struct {
	SchemaVersion    int               `json:"schema_version"`
	Tool             string            `json:"tool"`
	Scenario         string            `json:"scenario"`
	StartedAt        time.Time         `json:"started_at"`
	Target           string            `json:"target"`
	Requests         int               `json:"requests"`
	WarmupRequests   int               `json:"warmup_requests"`
	Concurrency      int               `json:"concurrency"`
	GatewayInstances int               `json:"gateway_instances"`
	DurationMS       float64           `json:"duration_ms"`
	ThroughputRPS    float64           `json:"throughput_rps"`
	LatencyMS        LatencySummary    `json:"latency_ms"`
	StatusCounts     map[string]int    `json:"status_counts"`
	TransportErrors  int               `json:"transport_errors"`
	HTTPErrors       int               `json:"http_errors"`
	ErrorRatePercent float64           `json:"error_rate_percent"`
	RateLimited      int               `json:"rate_limited"`
	UpstreamCounts   map[string]int    `json:"upstream_counts"`
	Verification     *Verification     `json:"verification,omitempty"`
	Environment      map[string]string `json:"environment"`
	Samples          []Sample          `json:"samples"`
}

type Metadata struct {
	Tool             string
	Scenario         string
	Target           string
	WarmupRequests   int
	Concurrency      int
	GatewayInstances int
	StartedAt        time.Time
	Duration         time.Duration
	Environment      map[string]string
}

func NewResult(metadata Metadata, samples []Sample) Result {
	statusCounts := make(map[string]int)
	upstreamCounts := make(map[string]int)
	latencies := make([]int64, 0, len(samples))
	transportErrors := 0
	httpErrors := 0
	rateLimited := 0
	for _, sample := range samples {
		latencies = append(latencies, sample.LatencyMicros)
		if sample.TransportError {
			transportErrors++
		} else {
			statusCounts[strconv.Itoa(sample.StatusCode)]++
		}
		if sample.StatusCode >= 400 {
			httpErrors++
		}
		if sample.StatusCode == 429 {
			rateLimited++
		}
		if sample.Upstream != "" {
			upstreamCounts[sample.Upstream]++
		}
	}

	durationSeconds := metadata.Duration.Seconds()
	throughput := 0.0
	if durationSeconds > 0 {
		throughput = float64(len(samples)) / durationSeconds
	}
	errorRate := 0.0
	if len(samples) > 0 {
		errorRate = 100 * float64(transportErrors+httpErrors) / float64(len(samples))
	}
	return Result{
		SchemaVersion:    SchemaVersion,
		Tool:             metadata.Tool,
		Scenario:         metadata.Scenario,
		StartedAt:        metadata.StartedAt,
		Target:           metadata.Target,
		Requests:         len(samples),
		WarmupRequests:   metadata.WarmupRequests,
		Concurrency:      metadata.Concurrency,
		GatewayInstances: metadata.GatewayInstances,
		DurationMS:       float64(metadata.Duration.Microseconds()) / 1000,
		ThroughputRPS:    throughput,
		LatencyMS:        SummarizeLatency(latencies),
		StatusCounts:     statusCounts,
		TransportErrors:  transportErrors,
		HTTPErrors:       httpErrors,
		ErrorRatePercent: errorRate,
		RateLimited:      rateLimited,
		UpstreamCounts:   upstreamCounts,
		Environment:      metadata.Environment,
		Samples:          samples,
	}
}

func SummarizeLatency(values []int64) LatencySummary {
	if len(values) == 0 {
		return LatencySummary{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total int64
	for _, value := range sorted {
		total += value
	}
	toMilliseconds := func(value int64) float64 { return float64(value) / 1000 }
	return LatencySummary{
		Minimum: toMilliseconds(sorted[0]),
		Mean:    toMilliseconds(total) / float64(len(sorted)),
		P50:     toMilliseconds(percentile(sorted, 0.50)),
		P95:     toMilliseconds(percentile(sorted, 0.95)),
		P99:     toMilliseconds(percentile(sorted, 0.99)),
		Maximum: toMilliseconds(sorted[len(sorted)-1]),
	}
}

func percentile(sorted []int64, quantile float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func Encode(writer io.Writer, result Result) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("encode benchmark result: %w", err)
	}
	return nil
}

func Decode(reader io.Reader) (Result, error) {
	var result Result
	if err := json.NewDecoder(reader).Decode(&result); err != nil {
		return Result{}, fmt.Errorf("decode benchmark result: %w", err)
	}
	if result.SchemaVersion != SchemaVersion {
		return Result{}, fmt.Errorf("unsupported benchmark schema version %d", result.SchemaVersion)
	}
	if result.Tool == "" || result.Scenario == "" || result.Requests <= 0 {
		return Result{}, fmt.Errorf("benchmark result is missing required metadata")
	}
	return result, nil
}
