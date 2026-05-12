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

	for _, ch := range []chan []byte{a, b} {
		select {
		case m := <-ch:
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
	case m := <-b:
		if string(m) != "x" {
			t.Fatalf("want x, got %s", m)
		}
	case <-time.After(time.Second):
		t.Fatal("b did not receive")
	}
	select {
	case <-a:
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
