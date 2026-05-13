package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"sharetext/internal/session"
	"sharetext/internal/store"
)

// Client→server message types. Empty Type with a Content field is treated as a
// legacy edit for backward compatibility with older clients.
const (
	wsTypeEdit      = "edit"
	wsTypeAcquire   = "lock_acquire"
	wsTypeHeartbeat = "lock_heartbeat"
	wsTypeRelease   = "lock_release"
)

type wsMsg struct {
	Type    string `json:"type,omitempty"`
	Content string `json:"content,omitempty"`
}

func (a *API) WS(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	ok, err := a.Store.Exists(r.Context(), slug)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer c.CloseNow()
	if MaxContentSize > 0 {
		c.SetReadLimit(MaxContentSize + 1024)
	}

	clientID, err := session.NewSlug(16)
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "id gen")
		return
	}

	ch := a.Hub.Join(slug)
	// Release any lock this client holds when the connection ends, then leave the room.
	// Lock release happens before Leave so peers (still in the room) receive the lock event.
	defer func() {
		if a.Locks != nil {
			if snap, released := a.Locks.Release(slug, clientID); released {
				a.broadcastLock(slug, snap, ch)
			}
		}
		a.Hub.Leave(slug, ch)
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Initial snapshot includes the freshly minted client_id and the current lock state.
	if sess, err := a.Store.Get(ctx, slug); err == nil {
		resp := toSessionResp(sess)
		resp.ClientID = clientID
		if a.Locks != nil {
			snap := a.Locks.State(slug)
			resp.Lock = &snap
		}
		init, _ := json.Marshal(resp)
		_ = c.Write(ctx, websocket.MessageText, init)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
				err := c.Write(wctx, websocket.MessageText, msg)
				wcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	writeBack := func(payload any) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		select {
		case ch <- data:
		default:
		}
	}

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m wsMsg
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		typ := m.Type
		if typ == "" {
			typ = wsTypeEdit
		}
		switch typ {
		case wsTypeEdit:
			if MaxContentSize > 0 && int64(len(m.Content)) > MaxContentSize {
				_ = c.Close(websocket.StatusMessageTooBig, "content too large")
				return
			}
			var lockChanged bool
			if a.Locks != nil {
				prev := a.Locks.State(slug)
				snap, granted := a.Locks.Acquire(slug, clientID)
				if !granted {
					writeBack(lockDeniedEvent{Type: "lock_denied", Lock: snap})
					continue
				}
				lockChanged = !prev.Held || prev.Holder != snap.Holder
			}
			sess, err := a.Store.Update(ctx, slug, m.Content)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrExpired) {
					return
				}
				continue
			}
			if a.Metrics != nil {
				a.Metrics.IncSessionUpdates()
			}
			resp := toSessionResp(sess)
			if a.Locks != nil {
				snap := a.Locks.State(slug)
				if snap.Held {
					resp.Lock = &snap
				}
			}
			out, _ := json.Marshal(resp)
			a.Hub.Broadcast(slug, out, ch)
			if lockChanged && a.Locks != nil {
				a.broadcastLock(slug, a.Locks.State(slug), nil)
			}
		case wsTypeAcquire:
			if a.Locks == nil {
				continue
			}
			prev := a.Locks.State(slug)
			snap, granted := a.Locks.Acquire(slug, clientID)
			if !granted {
				writeBack(lockDeniedEvent{Type: "lock_denied", Lock: snap})
				continue
			}
			if !prev.Held || prev.Holder != snap.Holder {
				a.broadcastLock(slug, snap, nil)
			} else {
				// Already held by us: confirm new TTL just to the caller.
				writeBack(lockEvent{Type: "lock", Lock: snap})
			}
		case wsTypeHeartbeat:
			if a.Locks == nil {
				continue
			}
			snap, refreshed := a.Locks.Heartbeat(slug, clientID)
			if !refreshed {
				// Caller is no longer the holder (likely TTL expired); inform.
				writeBack(lockDeniedEvent{Type: "lock_denied", Lock: snap})
			}
		case wsTypeRelease:
			if a.Locks == nil {
				continue
			}
			if snap, released := a.Locks.Release(slug, clientID); released {
				a.broadcastLock(slug, snap, nil)
			}
		default:
			// Unknown type → ignore.
		}
	}
}
