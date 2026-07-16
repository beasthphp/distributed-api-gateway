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
	ListenAddr           string
	UserServiceURL       string
	OrderServiceURL      string
	RedisAddr            string
	RedisPassword        string
	RedisDB              int
	DatabaseURL          string
	APIKeyPepper         string
	RateLimitPrefix      string
	RateLimitFailOpen    bool
	UsageQueueCapacity   int
	UsageBatchSize       int
	UsageFlushInterval   time.Duration
	UsageMaxAttempts     int
	UsageRetryBaseDelay  time.Duration
	UsageShutdownTimeout time.Duration
	ShutdownTimeout      time.Duration
}

// Load reads configuration from environment variables and applies safe local
// defaults. The default API key is for development only.
func Load() (Config, error) {
	cfg := Config{
		ListenAddr:           env("LISTEN_ADDR", ":8080"),
		UserServiceURL:       env("USER_SERVICE_URL", "http://localhost:8081"),
		OrderServiceURL:      env("ORDER_SERVICE_URL", "http://localhost:8082"),
		RedisAddr:            env("REDIS_ADDR", "localhost:6379"),
		RedisPassword:        os.Getenv("REDIS_PASSWORD"),
		DatabaseURL:          env("DATABASE_URL", "postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable"),
		APIKeyPepper:         env("API_KEY_PEPPER", "dev-only-pepper-change-me"),
		RateLimitPrefix:      env("RATE_LIMIT_PREFIX", "gateway:ratelimit"),
		RateLimitFailOpen:    envBool("RATE_LIMIT_FAIL_OPEN", false),
		UsageFlushInterval:   time.Second,
		UsageRetryBaseDelay:  100 * time.Millisecond,
		UsageShutdownTimeout: 5 * time.Second,
		ShutdownTimeout:      10 * time.Second,
	}

	var err error
	if cfg.RedisDB, err = envInt("REDIS_DB", 0); err != nil {
		return Config{}, err
	}
	if cfg.UsageQueueCapacity, err = envInt("USAGE_QUEUE_CAPACITY", 1024); err != nil {
		return Config{}, err
	}
	if cfg.UsageBatchSize, err = envInt("USAGE_BATCH_SIZE", 100); err != nil {
		return Config{}, err
	}
	if cfg.UsageMaxAttempts, err = envInt("USAGE_MAX_ATTEMPTS", 3); err != nil {
		return Config{}, err
	}
	if cfg.UsageFlushInterval, err = envDuration("USAGE_FLUSH_INTERVAL", cfg.UsageFlushInterval); err != nil {
		return Config{}, err
	}
	if cfg.UsageRetryBaseDelay, err = envDuration("USAGE_RETRY_BASE_DELAY", cfg.UsageRetryBaseDelay); err != nil {
		return Config{}, err
	}
	if cfg.UsageShutdownTimeout, err = envDuration("USAGE_SHUTDOWN_TIMEOUT", cfg.UsageShutdownTimeout); err != nil {
		return Config{}, err
	}

	if len(cfg.APIKeyPepper) < 16 {
		return Config{}, fmt.Errorf("API_KEY_PEPPER must contain at least 16 characters")
	}
	if cfg.UsageQueueCapacity <= 0 || cfg.UsageBatchSize <= 0 || cfg.UsageBatchSize > cfg.UsageQueueCapacity {
		return Config{}, fmt.Errorf("usage queue capacity and batch size must be positive, with batch size no larger than capacity")
	}
	if cfg.UsageMaxAttempts <= 0 || cfg.UsageFlushInterval <= 0 || cfg.UsageRetryBaseDelay <= 0 || cfg.UsageShutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("usage attempts and durations must be positive")
	}

	return cfg, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	return value, nil
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
