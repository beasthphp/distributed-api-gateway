package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/beasthphp/distributed-api-gateway/internal/auth"
	"github.com/beasthphp/distributed-api-gateway/internal/config"
	"github.com/beasthphp/distributed-api-gateway/internal/metrics"
	"github.com/beasthphp/distributed-api-gateway/internal/ratelimit"
	"github.com/beasthphp/distributed-api-gateway/internal/usage"
)

type contextKey string

const (
	principalContextKey contextKey = "principal"
	requestIDContextKey contextKey = "request-id"
	usageContextKey     contextKey = "usage-capture"
)

type HealthCheck struct {
	Name    string
	Checker ratelimit.HealthChecker
}

// Dependencies makes infrastructure replaceable in unit tests and keeps the
// HTTP layer independent from a concrete Redis implementation.
type Dependencies struct {
	Config    config.Config
	Limiter   ratelimit.Limiter
	Auth      auth.Authenticator
	Readiness []HealthCheck
	Metrics   *metrics.Registry
	Usage     usage.Recorder
	Logger    *slog.Logger
}

type handler struct {
	cfg        config.Config
	limiter    ratelimit.Limiter
	auth       auth.Authenticator
	readiness  []HealthCheck
	metrics    *metrics.Registry
	usage      usage.Recorder
	logger     *slog.Logger
	userProxy  *httputil.ReverseProxy
	orderProxy *httputil.ReverseProxy
}

// NewHandler assembles public operational endpoints and the protected proxy
// pipeline. Middleware order is request ID -> recovery -> metrics/logging ->
// authentication -> rate limiting -> routing.
func NewHandler(deps Dependencies) (http.Handler, error) {
	userTarget, err := url.Parse(deps.Config.UserServiceURL)
	if err != nil {
		return nil, fmt.Errorf("parse USER_SERVICE_URL: %w", err)
	}
	if userTarget.Scheme == "" || userTarget.Host == "" {
		return nil, fmt.Errorf("USER_SERVICE_URL must be an absolute URL")
	}
	orderTarget, err := url.Parse(deps.Config.OrderServiceURL)
	if err != nil {
		return nil, fmt.Errorf("parse ORDER_SERVICE_URL: %w", err)
	}
	if orderTarget.Scheme == "" || orderTarget.Host == "" {
		return nil, fmt.Errorf("ORDER_SERVICE_URL must be an absolute URL")
	}

	if deps.Metrics == nil {
		deps.Metrics = metrics.NewRegistry()
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}

	h := &handler{
		cfg:        deps.Config,
		limiter:    deps.Limiter,
		auth:       deps.Auth,
		readiness:  deps.Readiness,
		metrics:    deps.Metrics,
		usage:      deps.Usage,
		logger:     deps.Logger,
		userProxy:  newProxy("user-service", userTarget, deps.Logger),
		orderProxy: newProxy("order-service", orderTarget, deps.Logger),
	}

	var api http.Handler = http.HandlerFunc(h.route)
	api = h.rateLimit(api)
	api = h.authenticate(api)
	api = h.recoverPanic(api)
	api = h.instrument(api)
	api = h.requestID(api)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", h.live)
	mux.HandleFunc("GET /health/ready", h.ready)
	mux.Handle("GET /metrics", h.metrics)
	mux.Handle("/api/", api)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "route not found"})
	})
	return mux, nil
}

func newProxy(service string, target *url.URL, logger *slog.Logger) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		incomingHost := r.Host
		originalDirector(r)
		r.Host = target.Host
		r.Header.Set("X-Forwarded-Host", incomingHost)
		r.Header.Set("X-Gateway-Request-ID", requestIDFromContext(r.Context()))
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Set("X-Upstream-Service", service)
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("upstream request failed",
			"service", service,
			"request_id", requestIDFromContext(r.Context()),
			"error", err,
		)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream service unavailable"})
	}
	return proxy
}

func (h *handler) route(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/users" || strings.HasPrefix(r.URL.Path, "/api/users/"):
		h.userProxy.ServeHTTP(w, r)
	case r.URL.Path == "/api/orders" || strings.HasPrefix(r.URL.Path, "/api/orders/"):
		h.orderProxy.ServeHTTP(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "API route not found"})
	}
}

func (h *handler) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) ready(w http.ResponseWriter, r *http.Request) {
	if len(h.readiness) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready", "error": "dependencies are not configured"})
		return
	}
	for _, check := range h.readiness {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		err := check.Checker.Ping(ctx)
		cancel()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not ready",
				"error":  check.Name + " is unavailable",
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate := r.Header.Get("X-API-Key")
		if h.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "authentication service unavailable"})
			return
		}
		principal, err := h.auth.Authenticate(r.Context(), candidate, routeKey(r.URL.Path))
		if errors.Is(err, auth.ErrInvalidKey) {
			w.Header().Set("WWW-Authenticate", "ApiKey")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid API key"})
			return
		}
		if err != nil {
			h.logger.Error("API key lookup failed", "request_id", requestIDFromContext(r.Context()), "error", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "authentication service unavailable"})
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey, principal)
		if capture, ok := ctx.Value(usageContextKey).(*usageCapture); ok {
			capture.principal = principal
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *handler) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.limiter == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate limiter unavailable"})
			return
		}

		principal, ok := r.Context().Value(principalContextKey).(auth.Principal)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authenticated principal missing"})
			return
		}
		decision, err := h.limiter.Allow(r.Context(), ratelimit.Request{
			Identity:      principal.KeyID,
			Route:         routeKey(r.URL.Path),
			RatePerSecond: principal.RatePerSecond,
			BurstCapacity: principal.BurstCapacity,
		})
		if err != nil {
			h.logger.Error("rate limiter failed", "request_id", requestIDFromContext(r.Context()), "error", err)
			if h.cfg.RateLimitFailOpen {
				next.ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate limiter unavailable"})
			return
		}

		w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(decision.Limit, 10))
		w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(decision.Remaining, 10))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(decision.ResetAt.Unix(), 10))
		w.Header().Set("X-RateLimit-Policy", fmt.Sprintf("%d;r=%.0f", principal.BurstCapacity, float64(principal.RatePerSecond)))
		if !decision.Allowed {
			retryAfter := decision.RetryAfter
			seconds := int64((retryAfter + time.Second - 1) / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDContextKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *handler) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		capture := &usageCapture{}
		r = r.WithContext(context.WithValue(r.Context(), usageContextKey, capture))
		finish := h.metrics.Begin()
		defer func() {
			duration := time.Since(started)
			finish(recorder.status, duration)
			h.enqueueUsage(r, capture.principal, recorder, started, duration)

			h.logger.Info("request completed",
				"request_id", requestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.status,
				"bytes", recorder.bytes,
				"duration_ms", duration.Milliseconds(),
			)
		}()
		next.ServeHTTP(recorder, r)
	})
}

func (h *handler) enqueueUsage(r *http.Request, principal auth.Principal, recorder *statusRecorder, started time.Time, duration time.Duration) {
	if h.usage == nil || principal.KeyID == "" || principal.ClientID == "" {
		return
	}
	eventID, err := usage.NewEventID()
	if err != nil {
		h.logger.Error("generate usage event ID", "request_id", requestIDFromContext(r.Context()), "error", err)
		return
	}
	h.usage.Enqueue(usage.Event{
		ID:             eventID,
		RequestID:      requestIDFromContext(r.Context()),
		APIKeyID:       principal.KeyID,
		ClientID:       principal.ClientID,
		Route:          routeKey(r.URL.Path),
		Method:         r.Method,
		StatusCode:     recorder.status,
		DurationMicros: duration.Microseconds(),
		ResponseBytes:  int64(recorder.bytes),
		OccurredAt:     started.UTC(),
	})
}

type usageCapture struct {
	principal auth.Principal
}

func (h *handler) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				h.logger.Error("request panic",
					"request_id", requestIDFromContext(r.Context()),
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(payload []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	written, err := r.ResponseWriter.Write(payload)
	r.bytes += written
	return written, err
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey).(string)
	return id
}

func routeKey(path string) string {
	switch {
	case path == "/api/users" || strings.HasPrefix(path, "/api/users/"):
		return "/api/users"
	case path == "/api/orders" || strings.HasPrefix(path, "/api/orders/"):
		return "/api/orders"
	default:
		return "/api/unknown"
	}
}

func newRequestID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
