package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"sharetext/internal/store"
)

// BasicAuth returns middleware that protects routes with HTTP Basic auth.
// If user or pass is empty the middleware returns 503 for every request
// (admin disabled). Credentials are compared in constant time.
func BasicAuth(user, pass, realm string) func(http.Handler) http.Handler {
	if realm == "" {
		realm = "Restricted"
	}
	if user == "" || pass == "" {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "admin disabled: set ADMIN_USER and ADMIN_PASS", http.StatusServiceUnavailable)
			})
		}
	}
	expU := []byte(user)
	expP := []byte(pass)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			uOK := ok && subtle.ConstantTimeCompare([]byte(u), expU) == 1
			pOK := ok && subtle.ConstantTimeCompare([]byte(p), expP) == 1
			if !uOK || !pOK {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, withAdminUser(r, u))
		})
	}
}

type adminSession struct {
	Slug        string     `json:"slug"`
	Name        string     `json:"name,omitempty"`
	Type        string     `json:"type"`
	ContentSize int        `json:"content_size"`
	FilesSize   int64      `json:"files_size"`
	FilesCount  int        `json:"files_count"`
	TotalSize   int64      `json:"total_size"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type adminListResp struct {
	Sessions []adminSession `json:"sessions"`
	Count    int            `json:"count"`
}

type adminAuditResp struct {
	Enabled bool               `json:"enabled"`
	Entries []store.AuditEntry `json:"entries"`
	Count   int                `json:"count"`
}

type adminMetricsResp struct {
	Metrics           any  `json:"metrics"`
	ActiveRooms       int  `json:"active_rooms"`
	ActiveConnections int  `json:"active_connections"`
	Enabled           bool `json:"enabled"`
}

// AdminList returns all non-expired sessions.
func (a *API) AdminList(w http.ResponseWriter, r *http.Request) {
	items, err := a.Store.ListActive(r.Context())
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := adminListResp{Sessions: make([]adminSession, 0, len(items))}
	for _, it := range items {
		typ := "persistent"
		if it.ExpiresAt != nil {
			typ = "temporary"
		}
		out.Sessions = append(out.Sessions, adminSession{
			Slug:        it.Slug,
			Name:        it.Name,
			Type:        typ,
			ContentSize: it.ContentSize,
			FilesSize:   it.FilesSize,
			FilesCount:  it.FilesCount,
			TotalSize:   it.TotalSize(),
			CreatedAt:   it.CreatedAt,
			UpdatedAt:   it.UpdatedAt,
			ExpiresAt:   it.ExpiresAt,
		})
	}
	out.Count = len(out.Sessions)
	writeJSON(w, http.StatusOK, out)
}

// AdminDelete removes a session permanently. Returns 204 on success, 404 when
// the session is unknown.
func (a *API) AdminDelete(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if err := a.Store.Delete(r.Context(), slug); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	_ = a.Store.RecordAudit(r.Context(), store.AuditEntry{
		Actor:   adminActor(r),
		Action:  "admin.delete_session",
		Target:  slug,
		Details: "",
	})
	// Best-effort: include the slug so the UI can confirm.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"deleted": slug})
}

func (a *API) AdminAudit(w http.ResponseWriter, r *http.Request) {
	limit := a.AuditLogDefaultLimit
	if limit <= 0 {
		limit = 50
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	entries, err := a.Store.ListAuditLogs(r.Context(), limit)
	if err != nil {
		http.Error(w, "audit failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, adminAuditResp{
		Enabled: a.Store.AuditEnabled(),
		Entries: entries,
		Count:   len(entries),
	})
}

func (a *API) AdminMetrics(w http.ResponseWriter, r *http.Request) {
	enabled := a.Metrics != nil && a.Metrics.Enabled()
	rooms := 0
	connections := 0
	if a.Hub != nil {
		rooms = a.Hub.ActiveRooms()
		connections = a.Hub.ActiveConnections()
	}
	var snapshot any = map[string]any{"enabled": false}
	if enabled {
		snapshot = a.Metrics.Snapshot()
	}
	writeJSON(w, http.StatusOK, adminMetricsResp{
		Metrics:           snapshot,
		ActiveRooms:       rooms,
		ActiveConnections: connections,
		Enabled:           enabled,
	})
}

func adminActor(r *http.Request) string {
	if user := AdminUserFromContext(r.Context()); user != "" {
		return user
	}
	return "admin"
}
