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
)

const (
	MinSessionMinutes = 1
	MaxSessionMinutes = 60 * 24 * 7 // 7 days
)

type API struct {
	Store   *store.Store
	Hub     *Hub
	SlugLen int
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
	Slug      string     `json:"slug"`
	Name      string     `json:"name,omitempty"`
	Content   string     `json:"content"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type updateReq struct {
	Content string `json:"content"`
}

func (a *API) CreateSession(w http.ResponseWriter, r *http.Request) {
	var body createReq
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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
	writeJSON(w, http.StatusOK, toSessionResp(sess))
}

func (a *API) UpdateSession(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var body updateReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	sess, err := a.Store.Update(r.Context(), slug, body.Content)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if a.Hub != nil {
		msg, _ := json.Marshal(toSessionResp(sess))
		a.Hub.Broadcast(slug, msg, nil)
	}
	writeJSON(w, http.StatusOK, toSessionResp(sess))
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
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
