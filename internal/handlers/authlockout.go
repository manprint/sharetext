package handlers

import (
	"sync"
	"time"
)

type authFailEntry struct {
	fails int
	until time.Time
	last  time.Time
}

// authLockout blocks a client IP for a while after repeated failed admin
// logins. Disabled when max <= 0.
type authLockout struct {
	max      int
	duration time.Duration
	mu       sync.Mutex
	m        map[string]*authFailEntry
}

func newAuthLockout(max int, d time.Duration) *authLockout {
	if d <= 0 {
		d = 15 * time.Minute
	}
	return &authLockout{max: max, duration: d, m: map[string]*authFailEntry{}}
}

// blocked returns the remaining lock time (0 if not locked).
func (l *authLockout) blocked(ip string) time.Duration {
	if l.max <= 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.m[ip]; e != nil && time.Now().Before(e.until) {
		return time.Until(e.until)
	}
	return 0
}

func (l *authLockout) fail(ip string) {
	if l.max <= 0 {
		return
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 4096 {
		for k, e := range l.m {
			if now.Sub(e.last) > l.duration {
				delete(l.m, k)
			}
		}
	}
	e := l.m[ip]
	if e == nil {
		e = &authFailEntry{}
		l.m[ip] = e
	}
	if now.Sub(e.last) > l.duration {
		e.fails = 0
	}
	e.last = now
	e.fails++
	if e.fails >= l.max {
		e.until = now.Add(l.duration)
		e.fails = 0
	}
}

func (l *authLockout) reset(ip string) {
	if l.max <= 0 {
		return
	}
	l.mu.Lock()
	delete(l.m, ip)
	l.mu.Unlock()
}
