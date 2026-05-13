package handlers

import (
	"sync"
	"time"
)

// DefaultLockTTL is the fallback editor-lock TTL.
const DefaultLockTTL = 15 * time.Second

// LockSnapshot is the wire-friendly view of a lock at a point in time.
// Held=false means the lock is free (Holder/ExpiresAt zero values).
type LockSnapshot struct {
	Held      bool      `json:"held"`
	Holder    string    `json:"holder,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// LockManager keeps mutually-exclusive editor locks keyed by slug.
// The holder is identified by an opaque clientID. Locks auto-expire
// after TTL so a vanished client cannot hold the room hostage.
type LockManager struct {
	mu    sync.Mutex
	locks map[string]*lockEntry
	ttl   time.Duration
	now   func() time.Time
}

type lockEntry struct {
	holder    string
	expiresAt time.Time
}

func NewLockManager(ttl time.Duration) *LockManager {
	if ttl <= 0 {
		ttl = DefaultLockTTL
	}
	return &LockManager{
		locks: make(map[string]*lockEntry),
		ttl:   ttl,
		now:   time.Now,
	}
}

// TTL reports the configured lock TTL.
func (lm *LockManager) TTL() time.Duration { return lm.ttl }

// State returns the current snapshot, lazily evicting expired entries.
func (lm *LockManager) State(slug string) LockSnapshot {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.stateLocked(slug)
}

func (lm *LockManager) stateLocked(slug string) LockSnapshot {
	e, ok := lm.locks[slug]
	if !ok {
		return LockSnapshot{}
	}
	if !lm.now().Before(e.expiresAt) {
		delete(lm.locks, slug)
		return LockSnapshot{}
	}
	return LockSnapshot{Held: true, Holder: e.holder, ExpiresAt: e.expiresAt}
}

// Acquire grants the lock to clientID when free or already held by it; otherwise denies.
// Returns the post-call snapshot and granted=true when caller now holds the lock.
// An empty clientID can never hold a lock; the call returns the current snapshot.
func (lm *LockManager) Acquire(slug, clientID string) (LockSnapshot, bool) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if clientID == "" {
		return lm.stateLocked(slug), false
	}
	e, ok := lm.locks[slug]
	if !ok || !lm.now().Before(e.expiresAt) || e.holder == clientID {
		ent := &lockEntry{holder: clientID, expiresAt: lm.now().Add(lm.ttl)}
		lm.locks[slug] = ent
		return LockSnapshot{Held: true, Holder: clientID, ExpiresAt: ent.expiresAt}, true
	}
	return LockSnapshot{Held: true, Holder: e.holder, ExpiresAt: e.expiresAt}, false
}

// Heartbeat extends TTL when caller is the current holder. Returns (snapshot, refreshed).
func (lm *LockManager) Heartbeat(slug, clientID string) (LockSnapshot, bool) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if clientID == "" {
		return lm.stateLocked(slug), false
	}
	e, ok := lm.locks[slug]
	if !ok || !lm.now().Before(e.expiresAt) {
		if ok {
			delete(lm.locks, slug)
		}
		return LockSnapshot{}, false
	}
	if e.holder != clientID {
		return LockSnapshot{Held: true, Holder: e.holder, ExpiresAt: e.expiresAt}, false
	}
	e.expiresAt = lm.now().Add(lm.ttl)
	return LockSnapshot{Held: true, Holder: e.holder, ExpiresAt: e.expiresAt}, true
}

// Release frees the lock iff caller is the current holder. Returns (snapshot, released).
func (lm *LockManager) Release(slug, clientID string) (LockSnapshot, bool) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if clientID == "" {
		return lm.stateLocked(slug), false
	}
	e, ok := lm.locks[slug]
	if !ok {
		return LockSnapshot{}, false
	}
	if !lm.now().Before(e.expiresAt) || e.holder == clientID {
		delete(lm.locks, slug)
		return LockSnapshot{}, true
	}
	return LockSnapshot{Held: true, Holder: e.holder, ExpiresAt: e.expiresAt}, false
}

// CanWrite reports whether a writer with the given clientID may mutate the session.
// Returns (snapshot, allowed). A free lock allows anyone; a held lock only allows the holder.
// CanWrite does not modify state; use Acquire to take ownership.
func (lm *LockManager) CanWrite(slug, clientID string) (LockSnapshot, bool) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	snap := lm.stateLocked(slug)
	if !snap.Held {
		return snap, true
	}
	if clientID != "" && snap.Holder == clientID {
		return snap, true
	}
	return snap, false
}

// Forget drops any lock for the given slug (e.g., on session deletion).
func (lm *LockManager) Forget(slug string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	delete(lm.locks, slug)
}
