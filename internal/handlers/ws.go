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

// DefaultWSReadTimeout is the liveness window used when API.WSReadTimeout is
// unset. A peer that shows no sign of life for this long is disconnected,
// which defends against slow-loris-style connections that tie up goroutines
// indefinitely. Liveness is proven by answering the server's pings (every
// browser does so automatically, even for a tab that only watches), so idle
// viewers are not dropped.
const DefaultWSReadTimeout = 90 * time.Second

// wsMsg is a client→server message. Seq is an optional client-chosen edit
// number echoed in the reply to that edit (ack, edit_rejected or lock_denied),
// so the client can tell which of its edits each reply is about.
type wsMsg struct {
	Type    string `json:"type,omitempty"`
	Content string `json:"content,omitempty"`
	Seq     int64  `json:"seq,omitempty"`
}

// wsAck confirms to the sender that an edit was persisted. Together with the
// rejections below every edit gets exactly one reply, so the client knows what
// the server holds; an edit that never gets one is re-sent after reconnecting.
type wsAck struct {
	Type string `json:"type"` // always "ack"
	Seq  int64  `json:"seq,omitempty"`
}

// wsEditRejected tells the sender an edit was not persisted for a reason other
// than the editor lock (which gets lock_denied).
type wsEditRejected struct {
	Type  string `json:"type"` // always "edit_rejected"
	Error string `json:"error"`
	Seq   int64  `json:"seq,omitempty"`
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

	// AllowedOrigins augments the same-origin default. The request Host is
	// always authorized by the library, so leaving the list empty enforces
	// strict same-origin and closes the cross-site WebSocket hijacking foot-
	// gun that `InsecureSkipVerify: true` would have left open.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: a.AllowedOrigins,
	})
	if err != nil {
		return
	}
	defer c.CloseNow()
	maxContent := a.maxContentSize()
	c.SetReadLimit(maxContent + 1024)

	clientID, err := session.NewSlug(16)
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "id gen")
		return
	}

	sub := a.Hub.Join(slug)
	// Release any lock this client holds when the connection ends, then leave the room.
	// Lock release happens before Leave so peers (still in the room) receive the lock event.
	defer func() {
		if a.Locks != nil {
			a.Locks.Release(slug, clientID)
		}
		a.Hub.Leave(slug, sub)
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Initial snapshot includes the freshly minted client_id and the current lock state.
	// It is written before the writer goroutine starts; broadcasts that arrive
	// meanwhile wait in the subscriber buffer, so none is lost.
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
			case <-sub.Done():
				// Dropped by the hub (fell behind, or the session is gone):
				// close so the client reconnects from a fresh snapshot.
				_ = c.Close(websocket.StatusTryAgainLater, "resync")
				cancel()
				return
			case msg, ok := <-sub.C:
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

	// Keepalive. c.Read only returns on data frames, so a read deadline would
	// also kill viewers that legitimately never send anything. Pings are
	// answered by the peer's WebSocket stack (pongs are consumed by the read
	// loop below); a peer that leaves one unanswered for half the liveness
	// window, after up to another half of silence, is gone.
	pingEvery := a.wsReadTimeout() / 2
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(ctx, pingEvery)
				err := c.Ping(pctx)
				pcancel()
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
		sub.send(data)
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
			if int64(len(m.Content)) > maxContent {
				_ = c.Close(websocket.StatusMessageTooBig, "content too large")
				return
			}
			if a.Locks != nil {
				// A change of holder is announced after the content broadcast
				// below, so peers see the edit first (simple clients that only
				// look at content keep working).
				if snap, granted, _ := a.Locks.acquire(slug, clientID, false); !granted {
					writeBack(lockDeniedEvent{Type: "lock_denied", Lock: snap, Seq: m.Seq})
					continue
				}
			}
			sess, err := a.Store.Update(ctx, slug, m.Content)
			if err != nil {
				if a.Locks != nil {
					a.Locks.announce(slug)
				}
				if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrExpired) {
					return
				}
				if ctx.Err() != nil {
					return
				}
				reason := "internal error"
				if errors.Is(err, store.ErrSessionStorageExceeded) {
					reason = err.Error()
				}
				writeBack(wsEditRejected{Type: "edit_rejected", Error: reason, Seq: m.Seq})
				continue
			}
			if a.Metrics != nil {
				a.Metrics.IncSessionUpdates()
			}
			resp := toSessionResp(sess)
			if snap, held := a.lockState(slug); held {
				resp.Lock = &snap
			}
			out, _ := json.Marshal(resp)
			a.Hub.Broadcast(slug, out, sub)
			if a.Locks != nil {
				a.Locks.announce(slug)
			}
			writeBack(wsAck{Type: "ack", Seq: m.Seq})
		case wsTypeAcquire:
			if a.Locks == nil {
				continue
			}
			snap, granted, changed := a.Locks.acquire(slug, clientID, true)
			if !granted {
				writeBack(lockDeniedEvent{Type: "lock_denied", Lock: snap})
				continue
			}
			if !changed {
				// Already held by us: the room learnt nothing new, confirm
				// the refreshed TTL to the caller only.
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
				continue
			}
			// Hand back the new expiry so the client paces its next
			// heartbeat on the real deadline instead of the stale one.
			writeBack(lockEvent{Type: "lock", Lock: snap})
		case wsTypeRelease:
			if a.Locks == nil {
				continue
			}
			a.Locks.Release(slug, clientID)
		default:
			// Unknown type → ignore.
		}
	}
}
