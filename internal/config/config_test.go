package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("API_KEYS", "")
	t.Setenv("RATE_LIMIT_REQUESTS", "")
	t.Setenv("RATE_LIMIT_WINDOW", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.RateLimit != 100 {
		t.Fatalf("RateLimit = %d, want 100", cfg.RateLimit)
	}
}

func TestLoadRejectsInvalidLimit(t *testing.T) {
	t.Setenv("RATE_LIMIT_REQUESTS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid limit error")
	}
}

func TestLoadTrimsAPIKeys(t *testing.T) {
	t.Setenv("API_KEYS", " alpha, beta ,, ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.APIKeys) != 2 || cfg.APIKeys[0] != "alpha" || cfg.APIKeys[1] != "beta" {
		t.Fatalf("APIKeys = %#v, want [alpha beta]", cfg.APIKeys)
	}
}
