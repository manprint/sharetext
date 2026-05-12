package handlers

import "sync"

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[chan []byte]struct{}
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]map[chan []byte]struct{})}
}

func (h *Hub) Join(slug string) chan []byte {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.rooms[slug]; !ok {
		h.rooms[slug] = make(map[chan []byte]struct{})
	}
	h.rooms[slug][ch] = struct{}{}
	return ch
}

func (h *Hub) Leave(slug string, ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room, ok := h.rooms[slug]; ok {
		delete(room, ch)
		if len(room) == 0 {
			delete(h.rooms, slug)
		}
	}
	close(ch)
}

func (h *Hub) Broadcast(slug string, msg []byte, except chan []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.rooms[slug] {
		if ch == except {
			continue
		}
		select {
		case ch <- msg:
		default:
		}
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
