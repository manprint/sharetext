package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"sharetext/internal/session"
	"sharetext/internal/store"
	"sharetext/internal/telemetry"
)

const (
	MinSessionMinutes = 1
	MaxSessionMinutes = 60 * 24 * 7 // 7 days
)

// ClientIDHeader is the HTTP header carrying the editor-lock client identifier.
const ClientIDHeader = "X-Client-ID"

// 6 MiB leaves room for the ~34% inflation introduced by base64-encoded
// AES-GCM ciphertext while still capping the effective plaintext payload at
// roughly 4 MiB.
var MaxContentSize int64 = 6 * 1024 * 1024

type API struct {
	Store                *store.Store
	Hub                  *Hub
	Locks                *LockManager
	SlugLen              int
	Metrics              *telemetry.Metrics
	AuditLogDefaultLimit int
	// AllowedOrigins is forwarded to the WebSocket Accept call. Empty means
	// same-origin only (request Host is always authorized by the library).
	AllowedOrigins []string
}

type createReq struct {
	Type    string `json:"type"`              // "persistent" | "temporary"
	Name    string `json:"name,omitempty"`    // required for persistent
	Minutes int    `json:"minutes,omitempty"` // required for temporary
}

type createResp struct {
	Slug      string     `json:"slug"`
	URL       string     `json:"url"`
	Name      string     `json:"name,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type sessionResp struct {
	Slug      string        `json:"slug"`
	Name      string        `json:"name,omitempty"`
	Content   string        `json:"content"`
	UpdatedAt time.Time     `json:"updated_at"`
	ExpiresAt *time.Time    `json:"expires_at,omitempty"`
	Lock      *LockSnapshot `json:"lock,omitempty"`
	ClientID  string        `json:"client_id,omitempty"` // set only on the initial WS hello message
}

type updateReq struct {
	Content string `json:"content"`
}

// lockEvent is the wire payload pushed to WS peers whenever lock state changes.
type lockEvent struct {
	Type string       `json:"type"` // always "lock"
	Lock LockSnapshot `json:"lock"`
}

// lockDeniedEvent is sent only to the requesting client when a write was rejected
// because someone else holds the lock.
type lockDeniedEvent struct {
	Type string       `json:"type"` // always "lock_denied"
	Lock LockSnapshot `json:"lock"`
}

func (a *API) CreateSession(w http.ResponseWriter, r *http.Request) {
	var body createReq
	if r.ContentLength > 0 {
		if err := decodeJSONBody(w, r, &body, 64*1024); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
	}
	typ := strings.ToLower(strings.TrimSpace(body.Type))
	if typ == "" {
		typ = "temporary"
	}

	var name string
	var expiresAt *time.Time

	switch typ {
	case "persistent":
		name = strings.TrimSpace(body.Name)
		if !session.ValidName(name) {
			http.Error(w, "invalid name: required 1-32 chars [A-Za-z0-9_-]", http.StatusBadRequest)
			return
		}
	case "temporary":
		if body.Minutes < MinSessionMinutes || body.Minutes > MaxSessionMinutes {
			http.Error(w, "invalid minutes: must be between 1 and 10080", http.StatusBadRequest)
			return
		}
		t := time.Now().UTC().Add(time.Duration(body.Minutes) * time.Minute)
		expiresAt = &t
	default:
		http.Error(w, "type must be persistent or temporary", http.StatusBadRequest)
		return
	}

	slug, err := session.Compose(name, a.SlugLen)
	if err != nil {
		http.Error(w, "slug gen failed", http.StatusInternalServerError)
		return
	}
	sess, err := a.Store.Create(r.Context(), store.CreateOpts{
		Slug:      slug,
		Name:      name,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}
	if a.Metrics != nil {
		a.Metrics.IncSessionsCreated()
	}
	writeJSON(w, http.StatusCreated, createResp{
		Slug:      sess.Slug,
		URL:       "/s/" + sess.Slug,
		Name:      sess.Name,
		ExpiresAt: sess.ExpiresAt,
	})
}

func (a *API) GetSession(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	sess, err := a.Store.Get(r.Context(), slug)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	resp := toSessionResp(sess)
	if snap, ok := a.lockState(slug); ok {
		resp.Lock = &snap
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) UpdateSession(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var body updateReq
	if err := decodeJSONBody(w, r, &body, MaxContentSize+1024); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}
	if MaxContentSize > 0 && int64(len(body.Content)) > MaxContentSize {
		http.Error(w, "content too large", http.StatusRequestEntityTooLarge)
		return
	}
	clientID := strings.TrimSpace(r.Header.Get(ClientIDHeader))
	snap, lockChanged, ok := a.tryWriteLock(slug, clientID)
	if !ok {
		writeLockDenied(w, snap)
		return
	}

	sess, err := a.Store.Update(r.Context(), slug, body.Content)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if a.Metrics != nil {
		a.Metrics.IncSessionUpdates()
	}
	resp := toSessionResp(sess)
	if a.Locks != nil {
		current := a.Locks.State(slug)
		if current.Held {
			resp.Lock = &current
		}
	}
	if a.Hub != nil {
		msg, _ := json.Marshal(resp)
		a.Hub.Broadcast(slug, msg, nil)
		if lockChanged && a.Locks != nil {
			a.broadcastLock(slug, a.Locks.State(slug), nil)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// tryWriteLock encodes the "write-locks-the-editor" contract for HTTP mutators.
// If clientID is non-empty, it attempts to acquire ownership; on failure the
// caller is rejected with the current snapshot. If clientID is empty, it falls
// back to CanWrite semantics (allowed only when nobody else holds the lock).
// Returns (snapshot, lockStateChanged, allowed).
func (a *API) tryWriteLock(slug, clientID string) (LockSnapshot, bool, bool) {
	if a.Locks == nil {
		return LockSnapshot{}, false, true
	}
	prev := a.Locks.State(slug)
	if clientID == "" {
		_, allowed := a.Locks.CanWrite(slug, "")
		return prev, false, allowed
	}
	snap, granted := a.Locks.Acquire(slug, clientID)
	if !granted {
		return snap, false, false
	}
	changed := !prev.Held || prev.Holder != snap.Holder
	return snap, changed, true
}

func (a *API) lockState(slug string) (LockSnapshot, bool) {
	if a.Locks == nil {
		return LockSnapshot{}, false
	}
	snap := a.Locks.State(slug)
	return snap, snap.Held
}

func (a *API) broadcastLock(slug string, snap LockSnapshot, except chan []byte) {
	if a.Hub == nil {
		return
	}
	msg, err := json.Marshal(lockEvent{Type: "lock", Lock: snap})
	if err != nil {
		return
	}
	a.Hub.Broadcast(slug, msg, except)
}

func writeLockDenied(w http.ResponseWriter, snap LockSnapshot) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": "editor locked by another user",
		"lock":  snap,
	})
}

func toSessionResp(s *store.Session) sessionResp {
	return sessionResp{
		Slug:      s.Slug,
		Name:      s.Name,
		Content:   s.Content,
		UpdatedAt: s.UpdatedAt,
		ExpiresAt: s.ExpiresAt,
	}
}

func writeStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, store.ErrExpired):
		http.Error(w, "session expired", http.StatusGone)
	case errors.Is(err, store.ErrTooManyFiles), errors.Is(err, store.ErrSessionStorageExceeded):
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var errBodyTooLarge = errors.New("body too large")

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	if limit > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errBodyTooLarge
		}
		return err
	}
	return nil
}
