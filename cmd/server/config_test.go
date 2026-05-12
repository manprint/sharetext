package main

import (
	"testing"
	"time"

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
	}
	cfg := loadConfigFromEnv(func(key string) string { return env[key] })
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
}
