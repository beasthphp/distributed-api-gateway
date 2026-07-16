package config

import "testing"

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
