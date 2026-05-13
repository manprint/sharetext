package handlers

import (
	"context"
	"math"
	"net"
	"net/http"
	"sync"
	"time"
)

type adminUserContextKey struct{}

func withAdminUser(r *http.Request, user string) *http.Request {
	ctx := context.WithValue(r.Context(), adminUserContextKey{}, user)
	return r.WithContext(ctx)
}

func AdminUserFromContext(ctx context.Context) string {
	user, _ := ctx.Value(adminUserContextKey{}).(string)
	return user
}

type SecurityHeadersConfig struct {
	Enabled                 bool
	ContentSecurityPolicy   string
	FrameOptions            string
	ReferrerPolicy          string
	PermissionsPolicy       string
	StrictTransportSecurity string
}

func SecurityHeaders(cfg SecurityHeadersConfig) func(http.Handler) http.Handler {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.ContentSecurityPolicy != "" {
				w.Header().Set("Content-Security-Policy", cfg.ContentSecurityPolicy)
			}
			if cfg.FrameOptions != "" {
				w.Header().Set("X-Frame-Options", cfg.FrameOptions)
			}
			if cfg.ReferrerPolicy != "" {
				w.Header().Set("Referrer-Policy", cfg.ReferrerPolicy)
			}
			if cfg.PermissionsPolicy != "" {
				w.Header().Set("Permissions-Policy", cfg.PermissionsPolicy)
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if cfg.StrictTransportSecurity != "" && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
				w.Header().Set("Strict-Transport-Security", cfg.StrictTransportSecurity)
			}
			next.ServeHTTP(w, r)
		})
	}
}

type RateLimitConfig struct {
	Enabled           bool
	RequestsPerSecond float64
	Burst             int
	EntryTTL          time.Duration
}

type rateBucket struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

type ipRateLimiter struct {
	mu      sync.Mutex
	config  RateLimitConfig
	buckets map[string]*rateBucket
	hits    uint64
}

func NewIPRateLimiter(cfg RateLimitConfig) func(http.Handler) http.Handler {
	if !cfg.Enabled || cfg.RequestsPerSecond <= 0 || cfg.Burst <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	if cfg.EntryTTL <= 0 {
		cfg.EntryTTL = 10 * time.Minute
	}
	l := &ipRateLimiter{config: cfg, buckets: make(map[string]*rateBucket)}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.allow(clientIP(r), time.Now()) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func (l *ipRateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits++
	if l.hits%256 == 0 {
		l.prune(now)
	}
	bucket, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &rateBucket{
			tokens:   math.Max(0, float64(l.config.Burst-1)),
			last:     now,
			lastSeen: now,
		}
		return true
	}
	elapsed := now.Sub(bucket.last).Seconds()
	bucket.tokens = math.Min(float64(l.config.Burst), bucket.tokens+elapsed*l.config.RequestsPerSecond)
	bucket.last = now
	bucket.lastSeen = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

func (l *ipRateLimiter) prune(now time.Time) {
	cutoff := now.Add(-l.config.EntryTTL)
	for key, bucket := range l.buckets {
		if bucket.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}
