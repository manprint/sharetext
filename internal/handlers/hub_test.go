package handlers

import (
	"testing"
	"time"
)

func TestHubBroadcast(t *testing.T) {
	h := NewHub()
	a := h.Join("room")
	b := h.Join("room")
	defer h.Leave("room", a)
	defer h.Leave("room", b)

	h.Broadcast("room", []byte("hi"), nil)

	for _, sub := range []*Subscriber{a, b} {
		select {
		case m := <-sub.C:
			if string(m) != "hi" {
				t.Fatalf("want hi, got %s", m)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for broadcast")
		}
	}
}

func TestHubBroadcastExcept(t *testing.T) {
	h := NewHub()
	a := h.Join("r")
	b := h.Join("r")
	defer h.Leave("r", a)
	defer h.Leave("r", b)

	h.Broadcast("r", []byte("x"), a)

	select {
	case m := <-b.C:
		if string(m) != "x" {
			t.Fatalf("want x, got %s", m)
		}
	case <-time.After(time.Second):
		t.Fatal("b did not receive")
	}
	select {
	case <-a.C:
		t.Fatal("a should not receive")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHubLeaveCleansRoom(t *testing.T) {
	h := NewHub()
	a := h.Join("r")
	h.Leave("r", a)
	h.mu.RLock()
	_, ok := h.rooms["r"]
	h.mu.RUnlock()
	if ok {
		t.Fatal("room should be gone after last leave")
	}
}

func TestHubKicksSlowSubscriber(t *testing.T) {
	h := NewHub()
	slow := h.Join("r")
	fast := h.Join("r")
	defer h.Leave("r", slow)
	defer h.Leave("r", fast)

	for i := 0; i <= subscriberBuffer; i++ {
		h.Broadcast("r", []byte("m"), nil)
		<-fast.C
	}
	select {
	case <-slow.Done():
	default:
		t.Fatal("a subscriber that overflowed its buffer must be kicked, not silently skipped")
	}
	select {
	case <-fast.Done():
		t.Fatal("a subscriber that keeps up must not be kicked")
	default:
	}
}

func TestHubKickRoom(t *testing.T) {
	h := NewHub()
	a := h.Join("r")
	other := h.Join("other")
	defer h.Leave("r", a)
	defer h.Leave("other", other)
	h.Kick("r")
	select {
	case <-a.Done():
	default:
		t.Fatal("Kick must drop every subscriber of the room")
	}
	select {
	case <-other.Done():
		t.Fatal("Kick must not touch other rooms")
	default:
	}
}
