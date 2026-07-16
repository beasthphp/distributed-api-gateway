package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("API_KEY_PEPPER", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.DatabaseURL == "" {
		t.Fatal("DatabaseURL is empty")
	}
	if cfg.UsageQueueCapacity != 1024 || cfg.UsageBatchSize != 100 {
		t.Fatalf("usage queue defaults = %d/%d, want 1024/100", cfg.UsageQueueCapacity, cfg.UsageBatchSize)
	}
}

func TestLoadRejectsInvalidUsageConfiguration(t *testing.T) {
	t.Setenv("USAGE_QUEUE_CAPACITY", "10")
	t.Setenv("USAGE_BATCH_SIZE", "20")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid usage queue error")
	}
}

func TestLoadReadsUsageDurations(t *testing.T) {
	t.Setenv("USAGE_FLUSH_INTERVAL", "250ms")
	t.Setenv("USAGE_RETRY_BASE_DELAY", "25ms")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UsageFlushInterval != 250*time.Millisecond || cfg.UsageRetryBaseDelay != 25*time.Millisecond {
		t.Fatalf("usage durations = %v/%v", cfg.UsageFlushInterval, cfg.UsageRetryBaseDelay)
	}
}

func TestLoadRejectsShortPepper(t *testing.T) {
	t.Setenv("API_KEY_PEPPER", "too-short")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want short pepper error")
	}
}

func TestLoadReadsDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example/test")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DatabaseURL != "postgres://example/test" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}
