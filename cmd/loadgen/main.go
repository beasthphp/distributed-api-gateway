package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beasthphp/distributed-api-gateway/internal/benchmark"
)

type options struct {
	target           string
	requests         int
	concurrency      int
	warmup           int
	timeout          time.Duration
	output           string
	scenario         string
	instances        int
	allowedStatuses  map[int]bool
	minimumUpstreams int
	verifyRateLimit  bool
	rate             int
	burst            int
}

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	apiKey := strings.TrimSpace(os.Getenv("API_KEY"))
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "error: API_KEY environment variable is required")
		os.Exit(2)
	}

	result, passed, err := run(opts, apiKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := writeResult(opts.output, result); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%s: %.2f req/s, p50 %.2f ms, p95 %.2f ms, p99 %.2f ms, errors %.2f%%\n",
		result.Scenario, result.ThroughputRPS, result.LatencyMS.P50, result.LatencyMS.P95,
		result.LatencyMS.P99, result.ErrorRatePercent)
	if !passed {
		os.Exit(1)
	}
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var opts options
	var allowed string
	flags.StringVar(&opts.target, "url", "http://127.0.0.1:8080/api/users", "target URL")
	flags.IntVar(&opts.requests, "requests", 2000, "measured request count")
	flags.IntVar(&opts.concurrency, "concurrency", 32, "worker count")
	flags.IntVar(&opts.warmup, "warmup", 100, "unmeasured warmup requests")
	flags.DurationVar(&opts.timeout, "timeout", 5*time.Second, "per-request timeout")
	flags.StringVar(&opts.output, "output", "-", "JSON output path or - for stdout")
	flags.StringVar(&opts.scenario, "scenario", "manual", "stable scenario name")
	flags.IntVar(&opts.instances, "instances", 1, "gateway instance count")
	flags.StringVar(&allowed, "allowed-statuses", "200", "comma-separated allowed HTTP statuses")
	flags.IntVar(&opts.minimumUpstreams, "min-upstreams", 0, "minimum distinct proxy upstreams")
	flags.BoolVar(&opts.verifyRateLimit, "verify-rate-limit", false, "verify concurrent token-bucket bounds and headers")
	flags.IntVar(&opts.rate, "rate", 0, "expected refill rate for verification")
	flags.IntVar(&opts.burst, "burst", 0, "expected burst capacity for verification")
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments")
	}
	if opts.requests <= 0 || opts.concurrency <= 0 || opts.warmup < 0 || opts.timeout <= 0 || opts.instances <= 0 {
		return options{}, fmt.Errorf("requests, concurrency, timeout, and instances must be positive; warmup cannot be negative")
	}
	if opts.verifyRateLimit && (opts.rate <= 0 || opts.burst <= 0) {
		return options{}, fmt.Errorf("--verify-rate-limit requires positive --rate and --burst")
	}
	opts.allowedStatuses = make(map[int]bool)
	for _, raw := range strings.Split(allowed, ",") {
		status, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || status < 100 || status > 599 {
			return options{}, fmt.Errorf("invalid allowed status %q", raw)
		}
		opts.allowedStatuses[status] = true
	}
	return opts, nil
}

func run(opts options, apiKey string) (benchmark.Result, bool, error) {
	transport := &http.Transport{
		DisableCompression:    true,
		MaxIdleConns:          opts.concurrency * 2,
		MaxIdleConnsPerHost:   opts.concurrency,
		MaxConnsPerHost:       opts.concurrency,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: opts.timeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}

	for index := 0; index < opts.warmup; index++ {
		_, err := request(client, opts, apiKey)
		if err != nil {
			return benchmark.Result{}, false, fmt.Errorf("warmup request %d: %w", index+1, err)
		}
	}

	samples := make([]benchmark.Sample, opts.requests)
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(opts.concurrency)
	startedAt := time.Now().UTC()
	wallStarted := time.Now()
	for worker := 0; worker < opts.concurrency; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				sample, requestErr := request(client, opts, apiKey)
				if requestErr != nil {
					sample.TransportError = true
				}
				samples[index] = sample
			}
		}()
	}
	for index := range samples {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	duration := time.Since(wallStarted)

	result := benchmark.NewResult(benchmark.Metadata{
		Tool: "go-loadgen", Scenario: opts.scenario, Target: opts.target,
		WarmupRequests: opts.warmup, Concurrency: opts.concurrency,
		GatewayInstances: opts.instances, StartedAt: startedAt, Duration: duration,
		Environment: map[string]string{
			"go_version": runtime.Version(), "os": runtime.GOOS, "architecture": runtime.GOARCH,
		},
	}, samples)
	passed := verify(&result, opts, duration)
	return result, passed, nil
}

func request(client *http.Client, opts options, apiKey string) (benchmark.Sample, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.target, nil)
	if err != nil {
		return benchmark.Sample{}, err
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Accept", "application/json")
	started := time.Now()
	response, err := client.Do(req)
	latency := time.Since(started).Microseconds()
	if err != nil {
		return benchmark.Sample{LatencyMicros: latency, TransportError: true}, nil
	}
	_, copyErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	headersValid := response.Header.Get("X-RateLimit-Limit") != "" &&
		response.Header.Get("X-RateLimit-Remaining") != "" &&
		response.Header.Get("X-RateLimit-Reset") != ""
	if response.StatusCode == http.StatusTooManyRequests {
		headersValid = headersValid && response.Header.Get("Retry-After") != ""
	}
	sample := benchmark.Sample{
		LatencyMicros: latency, StatusCode: response.StatusCode,
		RateLimitHeadersValid: headersValid,
		Upstream:              strings.TrimSpace(response.Header.Get("X-Benchmark-Upstream")),
	}
	return sample, errors.Join(copyErr, closeErr)
}

func verify(result *benchmark.Result, opts options, duration time.Duration) bool {
	checks := map[string]bool{
		"no_transport_errors":  result.TransportErrors == 0,
		"status_codes_allowed": true,
	}
	for rawStatus := range result.StatusCounts {
		status, _ := strconv.Atoi(rawStatus)
		if !opts.allowedStatuses[status] {
			checks["status_codes_allowed"] = false
		}
	}
	if opts.minimumUpstreams > 0 {
		checks["minimum_upstreams_observed"] = len(result.UpstreamCounts) >= opts.minimumUpstreams
	}

	verification := &benchmark.Verification{
		Checks: checks, Accepted: result.StatusCounts["200"], Denied: result.StatusCounts["429"],
		ObservedUpstreams: len(result.UpstreamCounts),
	}
	if opts.verifyRateLimit {
		maximum := opts.burst + int(float64(opts.rate)*duration.Seconds()+0.999999)
		verification.TheoreticalMaximumAccepted = maximum
		checks["accepted_within_token_bound"] = verification.Accepted <= maximum
		checks["some_requests_accepted"] = verification.Accepted > 0
		checks["excess_requests_denied"] = verification.Denied > 0
		headersValid := true
		for _, sample := range result.Samples {
			if (sample.StatusCode == 200 || sample.StatusCode == 429) && !sample.RateLimitHeadersValid {
				headersValid = false
				break
			}
		}
		checks["rate_limit_headers_present"] = headersValid
	}
	verification.Passed = true
	for _, passed := range checks {
		verification.Passed = verification.Passed && passed
	}
	result.Verification = verification
	return verification.Passed
}

func writeResult(path string, result benchmark.Result) error {
	if path == "-" {
		return benchmark.Encode(os.Stdout, result)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	encodeErr := benchmark.Encode(file, result)
	closeErr := file.Close()
	return errors.Join(encodeErr, closeErr)
}
