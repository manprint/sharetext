package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSecurityHeadersMiddleware(t *testing.T) {
	mw := SecurityHeaders(SecurityHeadersConfig{
		Enabled:               true,
		ContentSecurityPolicy: "default-src 'self'",
		FrameOptions:          "DENY",
		ReferrerPolicy:        "no-referrer",
		PermissionsPolicy:     "camera=()",
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if got := w.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Fatalf("unexpected CSP: %q", got)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("unexpected frame options: %q", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("unexpected referrer policy: %q", got)
	}
	if got := w.Header().Get("Permissions-Policy"); got != "camera=()" {
		t.Fatalf("unexpected permissions policy: %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("unexpected nosniff header: %q", got)
	}
}

func TestSecurityHeadersHSTSAppliedBehindProxyHTTPS(t *testing.T) {
	mw := SecurityHeaders(SecurityHeadersConfig{
		Enabled:                 true,
		StrictTransportSecurity: "max-age=31536000; includeSubDomains",
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if got := w.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Fatalf("HSTS not set behind proxy: %q", got)
	}
}

func TestSecurityHeadersHSTSOmittedOnPlainHTTP(t *testing.T) {
	mw := SecurityHeaders(SecurityHeadersConfig{
		Enabled:                 true,
		StrictTransportSecurity: "max-age=31536000",
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS should not leak on plain HTTP: %q", got)
	}
}

func TestIPRateLimiterBlocksBurst(t *testing.T) {
	mw := NewIPRateLimiter(RateLimitConfig{
		Enabled:           true,
		RequestsPerSecond: 0.1,
		Burst:             1,
		EntryTTL:          time.Minute,
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "203.0.113.10:1234"
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusNoContent {
		t.Fatalf("first request want 204, got %d", w1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "203.0.113.10:5678"
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request want 429, got %d", w2.Code)
	}
	if got := w2.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("missing retry-after header, got %q", got)
	}

	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.RemoteAddr = "203.0.113.11:9999"
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNoContent {
		t.Fatalf("different IP should pass, got %d", w3.Code)
	}
}
