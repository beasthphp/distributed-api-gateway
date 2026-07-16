package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains all runtime settings for the gateway. Environment variables
// keep the binary identical across local, CI, and production deployments.
type Config struct {
	ListenAddr        string
	UserServiceURL    string
	OrderServiceURL   string
	RedisAddr         string
	RedisPassword     string
	RedisDB           int
	APIKeys           []string
	RateLimit         int64
	RateWindow        time.Duration
	RateLimitPrefix   string
	RateLimitFailOpen bool
	ShutdownTimeout   time.Duration
}

// Load reads configuration from environment variables and applies safe local
// defaults. The default API key is for development only.
func Load() (Config, error) {
	cfg := Config{
		ListenAddr:        env("LISTEN_ADDR", ":8080"),
		UserServiceURL:    env("USER_SERVICE_URL", "http://localhost:8081"),
		OrderServiceURL:   env("ORDER_SERVICE_URL", "http://localhost:8082"),
		RedisAddr:         env("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     os.Getenv("REDIS_PASSWORD"),
		RateLimitPrefix:   env("RATE_LIMIT_PREFIX", "gateway:ratelimit"),
		RateLimitFailOpen: envBool("RATE_LIMIT_FAIL_OPEN", false),
		ShutdownTimeout:   10 * time.Second,
	}

	var err error
	if cfg.RedisDB, err = envInt("REDIS_DB", 0); err != nil {
		return Config{}, err
	}

	limit, err := envInt("RATE_LIMIT_REQUESTS", 100)
	if err != nil {
		return Config{}, err
	}
	if limit <= 0 {
		return Config{}, fmt.Errorf("RATE_LIMIT_REQUESTS must be positive")
	}
	cfg.RateLimit = int64(limit)

	window, err := time.ParseDuration(env("RATE_LIMIT_WINDOW", "1m"))
	if err != nil || window < time.Millisecond {
		return Config{}, fmt.Errorf("RATE_LIMIT_WINDOW must be at least 1ms")
	}
	cfg.RateWindow = window

	cfg.APIKeys = splitNonEmpty(env("API_KEYS", "dev-key-change-me"))
	if len(cfg.APIKeys) == 0 {
		return Config{}, fmt.Errorf("API_KEYS must contain at least one key")
	}

	return cfg, nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return value, nil
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func splitNonEmpty(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}
