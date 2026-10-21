package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	cases := []struct {
		spec    string
		want    []string
		wantErr bool
	}{
		{spec: "", want: nil},
		{spec: "none", want: nil},
		{spec: "10.1.2.3", want: []string{"10.1.2.3/32"}},
		{spec: " 10.1.2.0/24 , 2001:db8::/32 ", want: []string{"10.1.2.0/24", "2001:db8::/32"}},
		{spec: "10.1.2.3/24", want: []string{"10.1.2.0/24"}},
		{spec: "::ffff:10.0.0.1", want: []string{"10.0.0.1/32"}},
		{spec: "private", want: prefixStrings(PrivateNetworks)},
		{spec: "PRIVATE,203.0.113.7", want: append(prefixStrings(PrivateNetworks), "203.0.113.7/32")},
		{spec: "10.0.0.300", wantErr: true},
		{spec: "nginx", wantErr: true},
		{spec: "10.0.0.0/33", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			got, err := ParseTrustedProxies(tc.spec)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(prefixStrings(got)) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v, want %v", prefixStrings(got), tc.want)
			}
		})
	}
}

func prefixStrings(ps []netip.Prefix) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

func TestRealIP(t *testing.T) {
	private := PrivateNetworks
	cases := []struct {
		name    string
		trusted []netip.Prefix
		header  string
		remote  string
		headers map[string][]string
		want    string
	}{
		{
			name:    "untrusted peer cannot pick its address",
			trusted: private,
			remote:  "198.51.100.7:4242",
			headers: map[string][]string{
				"X-Forwarded-For": {"1.2.3.4"},
				"X-Real-Ip":       {"1.2.3.4"},
				"True-Client-Ip":  {"1.2.3.4"},
			},
			want: "198.51.100.7:4242",
		},
		{
			name:    "no trusted proxies",
			remote:  "127.0.0.1:4242",
			headers: map[string][]string{"X-Forwarded-For": {"1.2.3.4"}},
			want:    "127.0.0.1:4242",
		},
		{
			name:    "proxy on loopback",
			trusted: private,
			remote:  "127.0.0.1:4242",
			headers: map[string][]string{"X-Forwarded-For": {"203.0.113.9"}},
			want:    "203.0.113.9",
		},
		{
			name:    "client-supplied entries on the left are ignored",
			trusted: private,
			remote:  "172.17.0.1:4242",
			headers: map[string][]string{"X-Forwarded-For": {"6.6.6.6, 203.0.113.9"}},
			want:    "203.0.113.9",
		},
		{
			name:    "chain of trusted proxies",
			trusted: private,
			remote:  "10.0.0.2:4242",
			headers: map[string][]string{"X-Forwarded-For": {"6.6.6.6, 203.0.113.9, 10.0.0.5"}},
			want:    "203.0.113.9",
		},
		{
			name:    "several header lines form one list",
			trusted: private,
			remote:  "10.0.0.2:4242",
			headers: map[string][]string{"X-Forwarded-For": {"6.6.6.6", "203.0.113.9, 10.0.0.5"}},
			want:    "203.0.113.9",
		},
		{
			name:    "client on the private network behind the proxy",
			trusted: private,
			remote:  "127.0.0.1:4242",
			headers: map[string][]string{"X-Forwarded-For": {"192.168.1.20"}},
			want:    "192.168.1.20",
		},
		{
			name:    "True-Client-IP from a trusted proxy is not the configured header",
			trusted: private,
			remote:  "127.0.0.1:4242",
			headers: map[string][]string{"True-Client-Ip": {"1.2.3.4"}, "X-Forwarded-For": {"203.0.113.9"}},
			want:    "203.0.113.9",
		},
		{
			name:    "trusted proxy without the header",
			trusted: private,
			remote:  "127.0.0.1:4242",
			want:    "127.0.0.1:4242",
		},
		{
			name:    "garbage entry falls back to the peer",
			trusted: private,
			remote:  "127.0.0.1:4242",
			headers: map[string][]string{"X-Forwarded-For": {"203.0.113.9, not-an-ip"}},
			want:    "127.0.0.1:4242",
		},
		{
			name:    "entry with a port",
			trusted: private,
			remote:  "127.0.0.1:4242",
			headers: map[string][]string{"X-Forwarded-For": {"203.0.113.9:51000"}},
			want:    "203.0.113.9",
		},
		{
			name:    "IPv6 peer and client",
			trusted: private,
			remote:  "[::1]:4242",
			headers: map[string][]string{"X-Forwarded-For": {"2001:db8::7"}},
			want:    "2001:db8::7",
		},
		{
			name:    "IPv4-mapped peer from a dual-stack listener",
			trusted: private,
			remote:  "[::ffff:127.0.0.1]:4242",
			headers: map[string][]string{"X-Forwarded-For": {"203.0.113.9"}},
			want:    "203.0.113.9",
		},
		{
			name:    "X-Real-IP mode ignores X-Forwarded-For",
			trusted: private,
			header:  "x-real-ip",
			remote:  "127.0.0.1:4242",
			headers: map[string][]string{"X-Real-Ip": {"203.0.113.9"}, "X-Forwarded-For": {"6.6.6.6"}},
			want:    "203.0.113.9",
		},
		{
			name:    "explicit proxy address",
			trusted: mustPrefixes(t, "203.0.113.50"),
			remote:  "203.0.113.50:4242",
			headers: map[string][]string{"X-Forwarded-For": {"198.51.100.1"}},
			want:    "198.51.100.1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			h := RealIP(RealIPConfig{TrustedProxies: tc.trusted, Header: tc.header})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			for k, vs := range tc.headers {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Fatalf("RemoteAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

func mustPrefixes(t *testing.T, spec string) []netip.Prefix {
	t.Helper()
	p, err := ParseTrustedProxies(spec)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A client that rotates X-Forwarded-For must still share one bucket.
func TestRateLimitCannotBeDodgedWithForwardingHeaders(t *testing.T) {
	limiter := NewIPRateLimiter(RateLimitConfig{Enabled: true, RequestsPerSecond: 0.001, Burst: 2})
	h := RealIP(RealIPConfig{TrustedProxies: PrivateNetworks})(limiter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	codes := make([]int, 0, 4)
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "198.51.100.7:4242"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i+1))
		req.Header.Set("X-Real-IP", fmt.Sprintf("10.0.1.%d", i+1))
		req.Header.Set("True-Client-IP", fmt.Sprintf("10.0.2.%d", i+1))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if fmt.Sprint(codes) != fmt.Sprint([]int{200, 200, 429, 429}) {
		t.Fatalf("spoofed headers must not grant new buckets, got %v", codes)
	}

	// Behind a trusted proxy, distinct clients keep distinct buckets.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:4242"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("client %d behind the proxy was limited: %d", i, rec.Code)
		}
	}
}

func TestRealIPWarnsAboutUntrustedProxyOncePerPeer(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	h := RealIP(RealIPConfig{
		TrustedProxies: PrivateNetworks,
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	send := func(remote string, withHeader bool) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote
		if withHeader {
			req.Header.Set("X-Forwarded-For", "198.51.100.1")
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	send("203.0.113.50:1", true)
	send("203.0.113.50:2", true)
	send("203.0.113.51:1", false)
	send("127.0.0.1:1", true)
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || !strings.Contains(lines[0], "203.0.113.50") || !strings.Contains(lines[0], "TRUSTED_PROXIES") {
		t.Fatalf("expected one warning about 203.0.113.50, got %q", lines)
	}
}
