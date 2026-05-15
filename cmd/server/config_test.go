package main

import (
	"testing"
	"time"

	"sharetext/internal/handlers"
	"sharetext/internal/store"
)

func TestLoadConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"PORT":                      "9090",
		"DB_PATH":                   "/tmp/sharetext.db",
		"MAX_CONTENT_SIZE":          "8192",
		"MAX_FILES_PER_SESSION":     "12",
		"MAX_SESSION_STORAGE_BYTES": "65536",
		"FILE_STORAGE_BACKEND":      store.FileBackendFS,
		"FILE_STORAGE_DIR":          "/tmp/files",
		"RATE_LIMIT_ENABLED":        "false",
		"WRITE_TIMEOUT":             "45s",
		"IDLE_TIMEOUT":              "3m",
		"SECURITY_HEADERS_ENABLED":  "false",
		"METRICS_ENABLED":           "false",
		"AUDIT_LOG_ENABLED":         "false",
		"AUDIT_LOG_DEFAULT_LIMIT":   "75",
		"LOCK_TTL":                  "25s",
		"LOCK_IDLE_RELEASE":         "7s",
		"WS_READ_TIMEOUT":           "45s",
		"CREATE_RATE_LIMIT_RPS":     "2",
		"CREATE_RATE_LIMIT_BURST":   "10",
		"CREATE_RATE_LIMIT_TTL":     "15m",
	}
	cfg := loadConfigFromEnv(func(key string) string { return env[key] })
	if cfg.LockTTL != 25*time.Second {
		t.Fatalf("expected LOCK_TTL=25s, got %s", cfg.LockTTL)
	}
	if cfg.LockIdleRelease != 7*time.Second {
		t.Fatalf("expected LOCK_IDLE_RELEASE=7s, got %s", cfg.LockIdleRelease)
	}
	if cfg.WSReadTimeout != 45*time.Second {
		t.Fatalf("expected WS_READ_TIMEOUT=45s, got %s", cfg.WSReadTimeout)
	}
	if cfg.CreateRateLimitRPS != 2 || cfg.CreateRateLimitBurst != 10 || cfg.CreateRateLimitTTL != 15*time.Minute {
		t.Fatalf("unexpected CREATE rate limit config: %+v", cfg)
	}
	if !cfg.CreateRateLimitEnabled {
		t.Fatal("expected CreateRateLimitEnabled default true")
	}
	if cfg.Port != "9090" || cfg.DBPath != "/tmp/sharetext.db" {
		t.Fatalf("unexpected basic config: %+v", cfg)
	}
	if cfg.MaxContentSize != 8192 || cfg.MaxFilesPerSession != 12 || cfg.MaxSessionStorageBytes != 65536 {
		t.Fatalf("unexpected limits: %+v", cfg)
	}
	if cfg.FileStorageBackend != store.FileBackendFS || cfg.FileStorageDir != "/tmp/files" {
		t.Fatalf("unexpected storage config: %+v", cfg)
	}
	if cfg.RateLimitEnabled || cfg.SecurityHeadersEnabled || cfg.MetricsEnabled || cfg.AuditLogEnabled {
		t.Fatalf("unexpected booleans: %+v", cfg)
	}
	if cfg.WriteTimeout != 45*time.Second || cfg.IdleTimeout != 3*time.Minute || cfg.AuditLogDefaultLimit != 75 {
		t.Fatalf("unexpected parsed values: %+v", cfg)
	}
}

func TestLoadConfigFromEnvFallsBackOnInvalidValues(t *testing.T) {
	env := map[string]string{
		"MAX_CONTENT_SIZE":     "-1",
		"CLEANUP_INTERVAL":     "bogus",
		"FILE_STORAGE_BACKEND": "weird",
		"RATE_LIMIT_ENABLED":   "maybe",
		"READ_HEADER_TIMEOUT":  "-5s",
		"MAX_HEADER_BYTES":     "-10",
	}
	cfg := loadConfigFromEnv(func(key string) string { return env[key] })
	if cfg.MaxContentSize <= 0 {
		t.Fatalf("expected default max content size, got %d", cfg.MaxContentSize)
	}
	if cfg.CleanupInterval != 30*time.Second {
		t.Fatalf("expected default cleanup interval, got %s", cfg.CleanupInterval)
	}
	if cfg.FileStorageBackend != "weird" {
		t.Fatalf("config loader should preserve backend string for store validation, got %q", cfg.FileStorageBackend)
	}
	if !cfg.RateLimitEnabled {
		t.Fatal("expected default rate limit enabled")
	}
	if cfg.ReadHeaderTimeout != 5*time.Second || cfg.MaxHeaderBytes != 1<<20 {
		t.Fatalf("expected defaults for invalid timeout/header, got %+v", cfg)
	}
	if cfg.LockTTL != handlers.DefaultLockTTL {
		t.Fatalf("expected LOCK_TTL default %s, got %s", handlers.DefaultLockTTL, cfg.LockTTL)
	}
	if cfg.LockIdleRelease != 3*time.Second {
		t.Fatalf("expected LOCK_IDLE_RELEASE default 3s, got %s", cfg.LockIdleRelease)
	}
	if cfg.WSReadTimeout != 90*time.Second {
		t.Fatalf("expected WS_READ_TIMEOUT default 90s, got %s", cfg.WSReadTimeout)
	}
	if cfg.CreateRateLimitRPS != 1 || cfg.CreateRateLimitBurst != 5 {
		t.Fatalf("expected CREATE rate limit defaults rps=1 burst=5, got rps=%v burst=%d", cfg.CreateRateLimitRPS, cfg.CreateRateLimitBurst)
	}
}

func TestLoadConfigFromEnvLockIdleReleaseRejectsSubSecond(t *testing.T) {
	env := map[string]string{
		"LOCK_IDLE_RELEASE": "500ms",
	}
	cfg := loadConfigFromEnv(func(key string) string { return env[key] })
	if cfg.LockIdleRelease != 3*time.Second {
		t.Fatalf("expected fallback to 3s when LOCK_IDLE_RELEASE < 1s, got %s", cfg.LockIdleRelease)
	}
}
