package main

import (
	"strconv"
	"strings"
	"time"

	"sharetext/internal/handlers"
	"sharetext/internal/store"
)

type appConfig struct {
	Port                    string
	DBPath                  string
	SlugLen                 int
	CleanupInterval         time.Duration
	RequestTimeout          time.Duration
	FileGrace               time.Duration
	VacuumInterval          time.Duration
	LockTTL                 time.Duration
	MaxFileSize             int64
	MaxContentSize          int64
	MaxFilesPerSession      int
	MaxSessionStorageBytes  int64
	FileStorageBackend      string
	FileStorageDir          string
	AdminUser               string
	AdminPass               string
	RateLimitEnabled        bool
	RateLimitRPS            float64
	RateLimitBurst          int
	RateLimitTTL            time.Duration
	AdminRateLimitRPS       float64
	AdminRateLimitBurst     int
	AdminRateLimitTTL       time.Duration
	ReadHeaderTimeout       time.Duration
	WriteTimeout            time.Duration
	IdleTimeout             time.Duration
	MaxHeaderBytes          int
	SecurityHeadersEnabled  bool
	ContentSecurityPolicy   string
	FrameOptions            string
	ReferrerPolicy          string
	PermissionsPolicy       string
	StrictTransportSecurity string
	MetricsEnabled          bool
	AuditLogEnabled         bool
	AuditLogDefaultLimit    int
}

func loadConfigFromEnv(getenv func(string) string) appConfig {
	if getenv == nil {
		getenv = func(key string) string { return envOr(key, "") }
	}
	return appConfig{
		Port:                    envOrWith(getenv, "PORT", "8080"),
		DBPath:                  envOrWith(getenv, "DB_PATH", "sharetext.db"),
		SlugLen:                 intEnv(getenv, "SLUG_LEN", 16),
		CleanupInterval:         durationEnv(getenv, "CLEANUP_INTERVAL", 30*time.Second, 30*time.Second),
		RequestTimeout:          durationEnv(getenv, "REQUEST_TIMEOUT", 30*time.Second, 30*time.Second),
		FileGrace:               durationEnv(getenv, "FILE_GRACE", 60*time.Second, 60*time.Second),
		VacuumInterval:          durationEnv(getenv, "VACUUM_INTERVAL", 0, 0),
		LockTTL:                 durationEnv(getenv, "LOCK_TTL", handlers.DefaultLockTTL, time.Second),
		MaxFileSize:             int64Env(getenv, "MAX_FILE_SIZE", handlers.MaxFileSize),
		MaxContentSize:          int64Env(getenv, "MAX_CONTENT_SIZE", handlers.MaxContentSize),
		MaxFilesPerSession:      intEnv(getenv, "MAX_FILES_PER_SESSION", 256),
		MaxSessionStorageBytes:  int64Env(getenv, "MAX_SESSION_STORAGE_BYTES", 100*1024*1024),
		FileStorageBackend:      envOrWith(getenv, "FILE_STORAGE_BACKEND", store.FileBackendDB),
		FileStorageDir:          strings.TrimSpace(getenv("FILE_STORAGE_DIR")),
		AdminUser:               strings.TrimSpace(getenv("ADMIN_USER")),
		AdminPass:               getenv("ADMIN_PASS"),
		RateLimitEnabled:        boolEnv(getenv, "RATE_LIMIT_ENABLED", true),
		RateLimitRPS:            floatEnv(getenv, "RATE_LIMIT_RPS", 20),
		RateLimitBurst:          intEnv(getenv, "RATE_LIMIT_BURST", 60),
		RateLimitTTL:            durationEnv(getenv, "RATE_LIMIT_TTL", 10*time.Minute, 10*time.Minute),
		AdminRateLimitRPS:       floatEnv(getenv, "ADMIN_RATE_LIMIT_RPS", 5),
		AdminRateLimitBurst:     intEnv(getenv, "ADMIN_RATE_LIMIT_BURST", 15),
		AdminRateLimitTTL:       durationEnv(getenv, "ADMIN_RATE_LIMIT_TTL", 10*time.Minute, 10*time.Minute),
		ReadHeaderTimeout:       durationEnv(getenv, "READ_HEADER_TIMEOUT", 5*time.Second, 5*time.Second),
		WriteTimeout:            durationEnv(getenv, "WRITE_TIMEOUT", 30*time.Second, 30*time.Second),
		IdleTimeout:             durationEnv(getenv, "IDLE_TIMEOUT", 2*time.Minute, 2*time.Minute),
		MaxHeaderBytes:          intEnv(getenv, "MAX_HEADER_BYTES", 1<<20),
		SecurityHeadersEnabled:  boolEnv(getenv, "SECURITY_HEADERS_ENABLED", true),
		ContentSecurityPolicy:   envOrWith(getenv, "CONTENT_SECURITY_POLICY", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self'; img-src 'self' data:; connect-src 'self' ws: wss:; worker-src 'self'; manifest-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"),
		FrameOptions:            envOrWith(getenv, "FRAME_OPTIONS", "DENY"),
		ReferrerPolicy:          envOrWith(getenv, "REFERRER_POLICY", "no-referrer"),
		PermissionsPolicy:       envOrWith(getenv, "PERMISSIONS_POLICY", "camera=(), microphone=(), geolocation=()"),
		StrictTransportSecurity: strings.TrimSpace(getenv("STRICT_TRANSPORT_SECURITY")),
		MetricsEnabled:          boolEnv(getenv, "METRICS_ENABLED", true),
		AuditLogEnabled:         boolEnv(getenv, "AUDIT_LOG_ENABLED", true),
		AuditLogDefaultLimit:    intEnv(getenv, "AUDIT_LOG_DEFAULT_LIMIT", 50),
	}
}

func (c appConfig) storeOptions() store.Options {
	return store.Options{
		FileBackend:            c.FileStorageBackend,
		FileStorageDir:         c.FileStorageDir,
		MaxFilesPerSession:     c.MaxFilesPerSession,
		MaxSessionStorageBytes: c.MaxSessionStorageBytes,
		AuditLogEnabled:        c.AuditLogEnabled,
	}
}

func envOrWith(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
}

func intEnv(getenv func(string) string, key string, def int) int {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

func int64Env(getenv func(string) string, key string, def int64) int64 {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

func floatEnv(getenv func(string) string, key string, def float64) float64 {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

func durationEnv(getenv func(string) string, key string, def, min time.Duration) time.Duration {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= min {
			return d
		}
	}
	return def
}

func boolEnv(getenv func(string) string, key string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(getenv(key)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
