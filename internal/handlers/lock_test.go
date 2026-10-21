package handlers

import (
	"sync"
	"testing"
	"time"
)

func newTestLM(ttl time.Duration) *LockManager {
	lm := NewLockManager(ttl)
	return lm
}

// withClock overrides the lock manager clock for deterministic tests.
func withClock(lm *LockManager, t time.Time) *LockManager {
	lm.now = func() time.Time { return t }
	return lm
}

func TestLockAcquireWhenFree(t *testing.T) {
	lm := newTestLM(time.Second)
	snap, granted := lm.Acquire("s", "A")
	if !granted {
		t.Fatal("want granted on free lock")
	}
	if !snap.Held || snap.Holder != "A" {
		t.Fatalf("want A holder, got %+v", snap)
	}
}

func TestLockAcquireIdempotentForHolder(t *testing.T) {
	lm := newTestLM(time.Second)
	if _, ok := lm.Acquire("s", "A"); !ok {
		t.Fatal("first acquire must succeed")
	}
	snap, granted := lm.Acquire("s", "A")
	if !granted || snap.Holder != "A" {
		t.Fatalf("re-acquire by holder must succeed, got %+v granted=%v", snap, granted)
	}
}

func TestLockAcquireDeniedToContender(t *testing.T) {
	lm := newTestLM(time.Second)
	if _, ok := lm.Acquire("s", "A"); !ok {
		t.Fatal("first acquire must succeed")
	}
	snap, granted := lm.Acquire("s", "B")
	if granted {
		t.Fatal("B must not acquire while A holds")
	}
	if snap.Holder != "A" {
		t.Fatalf("snapshot must still show A, got %+v", snap)
	}
}

func TestLockEmptyClientIDCannotHold(t *testing.T) {
	lm := newTestLM(time.Second)
	snap, granted := lm.Acquire("s", "")
	if granted {
		t.Fatal("empty client must never hold lock")
	}
	if snap.Held {
		t.Fatalf("snapshot must remain free, got %+v", snap)
	}
}

func TestLockExpiryReclaimsForContender(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	lm := withClock(NewLockManager(10*time.Second), base)
	lm.Acquire("s", "A")
	// jump past TTL
	lm.now = func() time.Time { return base.Add(11 * time.Second) }
	snap, granted := lm.Acquire("s", "B")
	if !granted || snap.Holder != "B" {
		t.Fatalf("B must acquire after expiry, got %+v granted=%v", snap, granted)
	}
}

func TestLockHeartbeatRefreshesTTL(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	lm := withClock(NewLockManager(10*time.Second), base)
	lm.Acquire("s", "A")
	// 5s elapsed: heartbeat extends to +15s from base
	lm.now = func() time.Time { return base.Add(5 * time.Second) }
	snap, refreshed := lm.Heartbeat("s", "A")
	if !refreshed {
		t.Fatal("heartbeat for holder must refresh")
	}
	want := base.Add(15 * time.Second)
	if !snap.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at: want %v, got %v", want, snap.ExpiresAt)
	}
}

func TestLockHeartbeatDeniedToNonHolder(t *testing.T) {
	lm := newTestLM(time.Second)
	lm.Acquire("s", "A")
	snap, refreshed := lm.Heartbeat("s", "B")
	if refreshed {
		t.Fatal("non-holder heartbeat must fail")
	}
	if snap.Holder != "A" {
		t.Fatalf("snapshot must still show A, got %+v", snap)
	}
}

func TestLockHeartbeatExpiredEntryDoesNotRevive(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	lm := withClock(NewLockManager(time.Second), base)
	lm.Acquire("s", "A")
	lm.now = func() time.Time { return base.Add(5 * time.Second) }
	snap, refreshed := lm.Heartbeat("s", "A")
	if refreshed {
		t.Fatal("expired lock must not be heartbeat-refreshed")
	}
	if snap.Held {
		t.Fatalf("expired lock should be free after heartbeat call, got %+v", snap)
	}
}

func TestLockReleaseByHolder(t *testing.T) {
	lm := newTestLM(time.Second)
	lm.Acquire("s", "A")
	snap, released := lm.Release("s", "A")
	if !released {
		t.Fatal("holder must release")
	}
	if snap.Held {
		t.Fatalf("post-release snapshot must be free, got %+v", snap)
	}
}

func TestLockReleaseByNonHolderNoop(t *testing.T) {
	lm := newTestLM(time.Second)
	lm.Acquire("s", "A")
	snap, released := lm.Release("s", "B")
	if released {
		t.Fatal("non-holder must not release")
	}
	if !snap.Held || snap.Holder != "A" {
		t.Fatalf("snapshot must still show A, got %+v", snap)
	}
}

func TestLockReleaseExpiredCleansUp(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	lm := withClock(NewLockManager(time.Second), base)
	lm.Acquire("s", "A")
	lm.now = func() time.Time { return base.Add(5 * time.Second) }
	_, released := lm.Release("s", "A")
	if !released {
		t.Fatal("releasing past-TTL holder should still clean up")
	}
	if lm.State("s").Held {
		t.Fatal("expected free after release")
	}
}

func TestLockCanWriteFreeAllowsAnyone(t *testing.T) {
	lm := newTestLM(time.Second)
	_, allowed := lm.CanWrite("s", "")
	if !allowed {
		t.Fatal("free lock must allow anonymous write")
	}
	_, allowed = lm.CanWrite("s", "anyone")
	if !allowed {
		t.Fatal("free lock must allow identified write")
	}
}

func TestLockCanWriteRespectsHolder(t *testing.T) {
	lm := newTestLM(time.Second)
	lm.Acquire("s", "A")
	if _, allowed := lm.CanWrite("s", "A"); !allowed {
		t.Fatal("holder must be allowed to write")
	}
	if _, allowed := lm.CanWrite("s", "B"); allowed {
		t.Fatal("non-holder must not be allowed to write")
	}
	if _, allowed := lm.CanWrite("s", ""); allowed {
		t.Fatal("anonymous must not be allowed to write while held")
	}
}

func TestLockForgetClearsState(t *testing.T) {
	lm := newTestLM(time.Second)
	lm.Acquire("s", "A")
	lm.Forget("s")
	if lm.State("s").Held {
		t.Fatal("Forget must drop the lock entry")
	}
}

func TestLockConcurrentAcquireOneWinner(t *testing.T) {
	lm := newTestLM(time.Second)
	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	winners := make(chan string, n)
	for i := 0; i < n; i++ {
		id := []byte{'C', byte('a' + i%26), byte('a' + (i/26)%26)}
		go func(cid string) {
			defer wg.Done()
			if _, ok := lm.Acquire("s", cid); ok {
				winners <- cid
			}
		}(string(id))
	}
	wg.Wait()
	close(winners)
	// All winners must share the same identity (the first acquirer wins,
	// further acquires by the same id remain successful).
	var first string
	count := 0
	for w := range winners {
		count++
		if first == "" {
			first = w
			continue
		}
		if w != first {
			t.Fatalf("multiple distinct winners: %q and %q", first, w)
		}
	}
	if count == 0 {
		t.Fatal("expected at least one winner")
	}
}

func TestLockTTLDefault(t *testing.T) {
	lm := NewLockManager(0)
	if lm.TTL() != DefaultLockTTL {
		t.Fatalf("want default TTL %v, got %v", DefaultLockTTL, lm.TTL())
	}
}

type lockEventLog struct {
	mu     sync.Mutex
	events []LockSnapshot
	ch     chan LockSnapshot
}

func watchLocks(lm *LockManager) *lockEventLog {
	l := &lockEventLog{ch: make(chan LockSnapshot, 1024)}
	lm.OnChange(func(slug string, snap LockSnapshot) {
		l.mu.Lock()
		l.events = append(l.events, snap)
		l.mu.Unlock()
		select {
		case l.ch <- snap:
		default: // the callback runs under the manager's mutex: never block
		}
	})
	return l
}

func (l *lockEventLog) next(t *testing.T, within time.Duration) LockSnapshot {
	t.Helper()
	select {
	case s := <-l.ch:
		return s
	case <-time.After(within):
		t.Fatalf("no lock event within %s", within)
		return LockSnapshot{}
	}
}

func (l *lockEventLog) none(t *testing.T, during time.Duration) {
	t.Helper()
	select {
	case s := <-l.ch:
		t.Fatalf("unexpected lock event %+v", s)
	case <-time.After(during):
	}
}

func TestLockOnChangeReportsTransitionsOnly(t *testing.T) {
	lm := NewLockManager(time.Minute)
	log := watchLocks(lm)
	lm.Acquire("s", "A")
	if ev := log.next(t, time.Second); !ev.Held || ev.Holder != "A" {
		t.Fatalf("acquire must announce A, got %+v", ev)
	}
	lm.Acquire("s", "A")
	lm.Heartbeat("s", "A")
	lm.Acquire("s", "B") // denied
	log.none(t, 50*time.Millisecond)
	lm.Release("s", "A")
	if ev := log.next(t, time.Second); ev.Held {
		t.Fatalf("release must announce free, got %+v", ev)
	}
}

func TestLockExpiryIsAnnounced(t *testing.T) {
	lm := NewLockManager(100 * time.Millisecond)
	log := watchLocks(lm)
	lm.Acquire("s", "A")
	log.next(t, time.Second)
	if ev := log.next(t, 2*time.Second); ev.Held {
		t.Fatalf("expiry must announce free, got %+v", ev)
	}
	if lm.State("s").Held {
		t.Fatal("lock must be free after the expiry event")
	}
}

func TestLockHeartbeatPostponesExpiry(t *testing.T) {
	lm := NewLockManager(150 * time.Millisecond)
	log := watchLocks(lm)
	lm.Acquire("s", "A")
	log.next(t, time.Second)
	for i := 0; i < 4; i++ {
		time.Sleep(75 * time.Millisecond)
		if _, ok := lm.Heartbeat("s", "A"); !ok {
			t.Fatalf("heartbeat %d refused", i)
		}
	}
	select {
	case ev := <-log.ch:
		t.Fatalf("heartbeating holder must keep the lock, got %+v", ev)
	default:
	}
	if ev := log.next(t, 2*time.Second); ev.Held {
		t.Fatalf("lock must expire once heartbeats stop, got %+v", ev)
	}
}

func TestLockReacquireAfterReleaseIsNotFreedByOldTimer(t *testing.T) {
	lm := NewLockManager(100 * time.Millisecond)
	log := watchLocks(lm)
	lm.Acquire("s", "A")
	log.next(t, time.Second)
	time.Sleep(60 * time.Millisecond)
	lm.Release("s", "A")
	log.next(t, time.Second)
	lm.Acquire("s", "B")
	log.next(t, time.Second)
	// A's timer would have fired ~40ms from now; B's lock must survive it.
	time.Sleep(70 * time.Millisecond)
	if snap := lm.State("s"); !snap.Held || snap.Holder != "B" {
		t.Fatalf("B's fresh lock must outlive A's stale timer, got %+v", snap)
	}
}

func TestLockEventsMatchFinalStateUnderConcurrency(t *testing.T) {
	lm := NewLockManager(time.Minute)
	log := watchLocks(lm)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if _, ok := lm.Acquire("s", id); ok {
					lm.Release("s", id)
				}
			}
		}(string(rune('A' + g)))
	}
	wg.Wait()
	lm.Acquire("s", "Z")
	log.mu.Lock()
	defer log.mu.Unlock()
	held := false
	holder := ""
	for i, ev := range log.events {
		if ev.Held == held {
			t.Fatalf("event %d repeats state held=%v: transitions out of order", i, held)
		}
		held, holder = ev.Held, ev.Holder
	}
	if snap := lm.State("s"); snap.Held != held || snap.Holder != holder {
		t.Fatalf("last event (%v,%q) disagrees with state %+v", held, holder, snap)
	}
}
