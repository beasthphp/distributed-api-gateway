package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beasthphp/distributed-api-gateway/internal/config"
	"github.com/beasthphp/distributed-api-gateway/internal/metrics"
	"github.com/beasthphp/distributed-api-gateway/internal/ratelimit"
)

type fakeLimiter struct {
	decision ratelimit.Decision
	err      error
}

func (f fakeLimiter) Allow(context.Context, string) (ratelimit.Decision, error) {
	return f.decision, f.err
}

type healthy struct{}

func (healthy) Ping(context.Context) error { return nil }

func TestProtectedRouteRequiresAPIKey(t *testing.T) {
	h := testHandler(t, "http://127.0.0.1:1", fakeLimiter{})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAuthorizedRequestIsProxied(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Request-ID") == "" {
			t.Error("gateway request ID was not forwarded")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service":"users"}`))
	}))
	defer upstream.Close()

	limiter := fakeLimiter{decision: ratelimit.Decision{
		Allowed: true, Limit: 10, Remaining: 9, ResetAt: time.Now().Add(time.Minute),
	}}
	h := testHandler(t, upstream.URL, limiter)
	request := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		body, _ := io.ReadAll(recorder.Body)
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, body)
	}
	if recorder.Header().Get("X-Upstream-Service") != "user-service" {
		t.Fatalf("X-Upstream-Service = %q", recorder.Header().Get("X-Upstream-Service"))
	}
}

func TestRateLimitDenialReturns429(t *testing.T) {
	limiter := fakeLimiter{decision: ratelimit.Decision{
		Allowed: false, Limit: 10, Remaining: 0, ResetAt: time.Now().Add(30 * time.Second),
	}}
	h := testHandler(t, "http://127.0.0.1:1", limiter)
	request := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	request.Header.Set("X-API-Key", "test-key")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header is missing")
	}
}

func TestReadinessIsPublic(t *testing.T) {
	h := testHandler(t, "http://127.0.0.1:1", fakeLimiter{})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func testHandler(t *testing.T, userURL string, limiter ratelimit.Limiter) http.Handler {
	t.Helper()
	h, err := NewHandler(Dependencies{
		Config: config.Config{
			APIKeys:         []string{"test-key"},
			UserServiceURL:  userURL,
			OrderServiceURL: userURL,
		},
		Limiter:   limiter,
		Readiness: healthy{},
		Metrics:   metrics.NewRegistry(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return h
}
