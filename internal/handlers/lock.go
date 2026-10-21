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
	mu       sync.Mutex
	locks    map[string]*lockEntry
	ttl      time.Duration
	now      func() time.Time
	onChange func(slug string, snap LockSnapshot)
	// announced is the holder last reported to onChange per slug (absent
	// means free). Announcements compare against it, so each one reports the
	// state at that instant and the stream can never end on a stale value.
	announced map[string]string
}

type lockEntry struct {
	holder    string
	expiresAt time.Time
	// timer fires at expiresAt to announce the expiry; nil when no OnChange
	// callback is registered (nobody would hear the announcement).
	timer *time.Timer
}

func (e *lockEntry) snapshot() LockSnapshot {
	return LockSnapshot{Held: true, Holder: e.holder, ExpiresAt: e.expiresAt}
}

func NewLockManager(ttl time.Duration) *LockManager {
	if ttl <= 0 {
		ttl = DefaultLockTTL
	}
	return &LockManager{
		locks:     make(map[string]*lockEntry),
		ttl:       ttl,
		now:       time.Now,
		announced: make(map[string]string),
	}
}

// TTL reports the configured lock TTL.
func (lm *LockManager) TTL() time.Duration { return lm.ttl }

// OnChange registers fn to be called whenever the lock of a slug changes
// hands: acquired by a new holder, released, or expired. A TTL refresh by the
// current holder is not a change.
//
// fn runs while the manager's mutex is held, so transitions are delivered in
// the exact order they happened even when they race across goroutines. It
// must therefore be fast, must not block and must not call back into the
// LockManager.
//
// Registering a callback also arms one timer per held lock, so a holder that
// stops heartbeating is announced as free as soon as its TTL lapses instead of
// whenever the next request happens to touch the lock. Register it before the
// manager is shared; locks acquired earlier get no timer.
func (lm *LockManager) OnChange(fn func(slug string, snap LockSnapshot)) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.onChange = fn
}

// State returns the current snapshot, lazily evicting expired entries.
func (lm *LockManager) State(slug string) LockSnapshot {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.stateLocked(slug)
}

func (lm *LockManager) stateLocked(slug string) LockSnapshot {
	e, ok := lm.liveLocked(slug)
	if !ok {
		return LockSnapshot{}
	}
	return e.snapshot()
}

// liveLocked returns the unexpired entry for slug, evicting (and announcing)
// an expired one on the way.
func (lm *LockManager) liveLocked(slug string) (*lockEntry, bool) {
	e, ok := lm.locks[slug]
	if !ok {
		return nil, false
	}
	if !lm.now().Before(e.expiresAt) {
		lm.evictLocked(slug, e)
		return nil, false
	}
	return e, true
}

// Acquire grants the lock to clientID when free or already held by it; otherwise denies.
// Returns the post-call snapshot and granted=true when caller now holds the lock.
// An empty clientID can never hold a lock; the call returns the current snapshot.
func (lm *LockManager) Acquire(slug, clientID string) (LockSnapshot, bool) {
	snap, granted, _ := lm.acquire(slug, clientID, true)
	return snap, granted
}

// acquire is Acquire plus changed=true when the lock passed to clientID with
// this call (as opposed to a TTL refresh of a lock clientID already held).
// With announce=false a new holder is not reported yet: the caller must call
// announce once it has broadcast what should reach peers first.
func (lm *LockManager) acquire(slug, clientID string, announce bool) (LockSnapshot, bool, bool) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if clientID == "" {
		return lm.stateLocked(slug), false, false
	}
	if e, ok := lm.liveLocked(slug); ok {
		if e.holder != clientID {
			return e.snapshot(), false, false
		}
		e.expiresAt = lm.now().Add(lm.ttl)
		return e.snapshot(), true, false
	}
	e := &lockEntry{holder: clientID, expiresAt: lm.now().Add(lm.ttl)}
	lm.locks[slug] = e
	if lm.onChange != nil {
		e.timer = time.AfterFunc(lm.ttl, func() { lm.expire(slug, e) })
	}
	if announce {
		lm.announceLocked(slug)
	}
	return e.snapshot(), true, true
}

// announce reports the current state of slug to onChange if it differs from
// the last report.
func (lm *LockManager) announce(slug string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.liveLocked(slug)
	lm.announceLocked(slug)
}

// Heartbeat extends TTL when caller is the current holder. Returns (snapshot, refreshed).
func (lm *LockManager) Heartbeat(slug, clientID string) (LockSnapshot, bool) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if clientID == "" {
		return lm.stateLocked(slug), false
	}
	e, ok := lm.liveLocked(slug)
	if !ok {
		return LockSnapshot{}, false
	}
	if e.holder != clientID {
		return e.snapshot(), false
	}
	// The expiry timer is not reset here: when it fires it sees the extended
	// deadline and re-arms itself for the remainder.
	e.expiresAt = lm.now().Add(lm.ttl)
	return e.snapshot(), true
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
		lm.evictLocked(slug, e)
		return LockSnapshot{}, true
	}
	return e.snapshot(), false
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

// Forget drops any lock for the given slug (e.g., on session deletion)
// without announcing it: the session, and therefore its room, is gone.
func (lm *LockManager) Forget(slug string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if e, ok := lm.locks[slug]; ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(lm.locks, slug)
	}
	delete(lm.announced, slug)
}

// expire runs on e's timer goroutine once its TTL may have lapsed.
func (lm *LockManager) expire(slug string, e *lockEntry) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.locks[slug] != e {
		// Released, forgotten or replaced by a newer holder meanwhile.
		return
	}
	if remaining := e.expiresAt.Sub(lm.now()); remaining > 0 {
		e.timer.Reset(remaining)
		return
	}
	lm.evictLocked(slug, e)
}

func (lm *LockManager) evictLocked(slug string, e *lockEntry) {
	if e.timer != nil {
		e.timer.Stop()
	}
	delete(lm.locks, slug)
	lm.announceLocked(slug)
}

func (lm *LockManager) announceLocked(slug string) {
	if lm.onChange == nil {
		return
	}
	var snap LockSnapshot
	if e, ok := lm.locks[slug]; ok {
		snap = e.snapshot()
	}
	if lm.announced[slug] == snap.Holder {
		return
	}
	if snap.Held {
		lm.announced[slug] = snap.Holder
	} else {
		delete(lm.announced, slug)
	}
	lm.onChange(slug, snap)
}
