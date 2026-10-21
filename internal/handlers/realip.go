package handlers

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const headerXForwardedFor = "X-Forwarded-For"

// PrivateNetworks are the peers trusted as reverse proxies by default:
// loopback, RFC 1918, IPv6 unique-local and link-local addresses. They cover a
// proxy on the same host (Docker hands it a bridge address) or on a private
// network, while a client connecting from the internet cannot choose its own
// address with a forwarding header.
var PrivateNetworks = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

// ParseTrustedProxies parses a comma-separated list of IP addresses and CIDR
// prefixes. The keyword "private" stands for PrivateNetworks; "none" adds
// nothing, so "none" alone trusts no proxy at all.
func ParseTrustedProxies(spec string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, raw := range strings.Split(spec, ",") {
		item := strings.TrimSpace(raw)
		switch strings.ToLower(item) {
		case "", "none":
			continue
		case "private":
			out = append(out, PrivateNetworks...)
			continue
		}
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q: %w", item, err)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", item, err)
		}
		a = normalizeAddr(a)
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// RealIPConfig configures RealIP.
type RealIPConfig struct {
	// TrustedProxies lists the peers whose forwarding header is believed.
	// Empty trusts nobody: the TCP peer is always taken as the client.
	TrustedProxies []netip.Prefix
	// Header carries the client address written by the trusted proxies.
	// X-Forwarded-For (the default) is read right to left: every proxy
	// appends the address it received the request from, so the rightmost
	// entry that is not a trusted proxy is the client, and whatever the
	// client put on the left is ignored. Any other header (e.g. X-Real-IP)
	// must hold one address that the proxy always overwrites.
	Header string
	// Logf, when set, reports peers that send forwarding headers without
	// being trusted, i.e. most likely a proxy missing from TrustedProxies
	// (every user then shares that proxy's rate-limit bucket). Each peer is
	// reported at most once an hour.
	Logf func(format string, args ...any)
}

// RealIP replaces chi's middleware.RealIP, which believes True-Client-IP,
// X-Real-IP and X-Forwarded-For from anyone: any client could pick its own
// address with a header and get a fresh per-IP rate-limit bucket on every
// request. Here the forwarding header counts only when the TCP peer is a
// trusted proxy. r.RemoteAddr is then replaced with the client address,
// without a port, as middleware.RealIP did.
func RealIP(cfg RealIPConfig) func(http.Handler) http.Handler {
	header := http.CanonicalHeaderKey(strings.TrimSpace(cfg.Header))
	if header == "" {
		header = headerXForwardedFor
	}
	warn := newUntrustedPeerWarner(cfg.Logf)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if peer, ok := peerAddr(r.RemoteAddr); ok {
				if containsAddr(cfg.TrustedProxies, peer) {
					if client, ok := forwardedClient(r.Header, header, cfg.TrustedProxies); ok {
						r.RemoteAddr = client.String()
					}
				} else if warn != nil && hasForwardingHeader(r.Header) {
					warn(peer)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func forwardedClient(h http.Header, header string, trusted []netip.Prefix) (netip.Addr, bool) {
	if header != headerXForwardedFor {
		return parseForwardedAddr(h.Get(header))
	}
	var hops []string
	for _, v := range h.Values(header) {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseForwardedAddr(hops[i])
		if !ok {
			// Entries from here leftwards cannot be attributed to a proxy
			// of ours; fall back to the peer address.
			return netip.Addr{}, false
		}
		// The leftmost entry is the client even when it is itself on a
		// trusted network (e.g. a LAN user behind the proxy).
		if i == 0 || !containsAddr(trusted, a) {
			return a, true
		}
	}
	return netip.Addr{}, false
}

func parseForwardedAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return normalizeAddr(a), true
	}
	// Some proxies include the client port: "1.2.3.4:5678", "[2001:db8::1]:80".
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return normalizeAddr(ap.Addr()), true
	}
	return netip.Addr{}, false
}

func peerAddr(remoteAddr string) (netip.Addr, bool) {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return normalizeAddr(a), true
}

// normalizeAddr maps IPv4-mapped IPv6 addresses (dual-stack listeners) back
// to IPv4 and drops zones, so prefixes match however the address arrived.
func normalizeAddr(a netip.Addr) netip.Addr {
	return a.Unmap().WithZone("")
}

func containsAddr(prefixes []netip.Prefix, a netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func hasForwardingHeader(h http.Header) bool {
	for _, k := range []string{headerXForwardedFor, "X-Real-Ip", "True-Client-Ip", "Forwarded"} {
		if h.Get(k) != "" {
			return true
		}
	}
	return false
}

func newUntrustedPeerWarner(logf func(string, ...any)) func(netip.Addr) {
	if logf == nil {
		return nil
	}
	var mu sync.Mutex
	seen := make(map[netip.Addr]time.Time)
	return func(peer netip.Addr) {
		now := time.Now()
		mu.Lock()
		if last, ok := seen[peer]; ok && now.Sub(last) < time.Hour {
			mu.Unlock()
			return
		}
		if len(seen) >= 1024 {
			clear(seen)
		}
		seen[peer] = now
		mu.Unlock()
		logf("realip: %s sent forwarding headers but is not in TRUSTED_PROXIES; headers ignored, using it as the client address", peer)
	}
}
