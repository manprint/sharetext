package handlers

import "sync"

// subscriberBuffer is how many undelivered messages a WebSocket peer may
// accumulate before the hub gives up on it.
const subscriberBuffer = 32

// Subscriber is one WebSocket peer of a room.
type Subscriber struct {
	// C carries the messages to deliver. Leave closes it.
	C    chan []byte
	done chan struct{}
	once sync.Once
}

// Done is closed when the hub drops the subscriber: it fell too far behind,
// or its room was shut down. Its connection should then be closed so the
// client reconnects and resynchronises from a fresh snapshot, instead of
// carrying on with a silently incomplete stream of updates.
func (s *Subscriber) Done() <-chan struct{} { return s.done }

func (s *Subscriber) kick() { s.once.Do(func() { close(s.done) }) }

// send queues msg without blocking. A subscriber whose buffer is full has
// already missed the chance to stay in sync, so it is kicked rather than
// having the message dropped. Callers must not race Leave (the hub serialises
// Broadcast against it; the WS handler only sends from its own read loop).
func (s *Subscriber) send(msg []byte) {
	select {
	case s.C <- msg:
	default:
		s.kick()
	}
}

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*Subscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]map[*Subscriber]struct{})}
}

func (h *Hub) Join(slug string) *Subscriber {
	sub := &Subscriber{C: make(chan []byte, subscriberBuffer), done: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.rooms[slug]; !ok {
		h.rooms[slug] = make(map[*Subscriber]struct{})
	}
	h.rooms[slug][sub] = struct{}{}
	return sub
}

func (h *Hub) Leave(slug string, sub *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room, ok := h.rooms[slug]; ok {
		delete(room, sub)
		if len(room) == 0 {
			delete(h.rooms, slug)
		}
	}
	close(sub.C)
}

func (h *Hub) Broadcast(slug string, msg []byte, except *Subscriber) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.rooms[slug] {
		if sub == except {
			continue
		}
		sub.send(msg)
	}
}

// Kick drops every subscriber of the room (e.g. the session was deleted).
func (h *Hub) Kick(slug string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.rooms[slug] {
		sub.kick()
	}
}

func (h *Hub) ActiveRooms() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms)
}

func (h *Hub) ActiveConnections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := 0
	for _, room := range h.rooms {
		total += len(room)
	}
	return total
}
