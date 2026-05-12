package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"sharetext/internal/store"
)

func newAdminRouter(t *testing.T, user, pass string) (*API, *chi.Mux) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := &API{Store: st, Hub: NewHub(), SlugLen: 16}
	r := chi.NewRouter()
	r.Group(func(g chi.Router) {
		g.Use(BasicAuth(user, pass, "admin"))
		g.Get("/admin/api/sessions", api.AdminList)
		g.Delete("/admin/api/sessions/{slug}", api.AdminDelete)
	})
	return api, r
}

func basicAuthHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func TestAdminAuthMissing(t *testing.T) {
	_, r := newAdminRouter(t, "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/sessions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("missing WWW-Authenticate header")
	}
}

func TestAdminAuthWrongPassword(t *testing.T) {
	_, r := newAdminRouter(t, "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/sessions", nil)
	req.Header.Set("Authorization", basicAuthHeader("admin", "nope"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAdminAuthWrongUser(t *testing.T) {
	_, r := newAdminRouter(t, "admin", "secret")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/sessions", nil)
	req.Header.Set("Authorization", basicAuthHeader("intruder", "secret"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAdminDisabledWhenCredsMissing(t *testing.T) {
	_, r := newAdminRouter(t, "", "")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/sessions", nil)
	req.Header.Set("Authorization", basicAuthHeader("any", "any"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", w.Code)
	}
}

func TestAdminListIncludesFilesSize(t *testing.T) {
	api, r := newAdminRouter(t, "admin", "secret")
	ctx := context.Background()
	if _, err := api.Store.Create(ctx, store.CreateOpts{Slug: "withfiles", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Store.Update(ctx, "withfiles", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Store.AddFile(ctx, "withfiles", "f1", "a.bin", "application/octet-stream", []byte("aaaaa")); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Store.AddFile(ctx, "withfiles", "f2", "b.bin", "application/octet-stream", []byte("bbbbbbb")); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/sessions", nil)
	req.Header.Set("Authorization", basicAuthHeader("admin", "secret"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp adminListResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(resp.Sessions))
	}
	s := resp.Sessions[0]
	if s.ContentSize != 5 {
		t.Errorf("content_size want 5, got %d", s.ContentSize)
	}
	if s.FilesSize != 12 {
		t.Errorf("files_size want 12, got %d", s.FilesSize)
	}
	if s.FilesCount != 2 {
		t.Errorf("files_count want 2, got %d", s.FilesCount)
	}
	if s.TotalSize != 17 {
		t.Errorf("total_size want 17, got %d", s.TotalSize)
	}
}

func TestAdminListExcludesExpired(t *testing.T) {
	api, r := newAdminRouter(t, "admin", "secret")
	past := time.Now().Add(-time.Hour).UTC()
	future := time.Now().Add(time.Hour).UTC()
	ctx := context.Background()
	if _, err := api.Store.Create(ctx, store.CreateOpts{Slug: "p1", Name: "team"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Store.Create(ctx, store.CreateOpts{Slug: "t1", ExpiresAt: &future}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Store.Create(ctx, store.CreateOpts{Slug: "old", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/sessions", nil)
	req.Header.Set("Authorization", basicAuthHeader("admin", "secret"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp adminListResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count != 2 || len(resp.Sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d: %+v", resp.Count, resp.Sessions)
	}
	types := map[string]string{}
	for _, s := range resp.Sessions {
		types[s.Slug] = s.Type
	}
	if types["p1"] != "persistent" {
		t.Fatalf("p1 should be persistent, got %q", types["p1"])
	}
	if types["t1"] != "temporary" {
		t.Fatalf("t1 should be temporary, got %q", types["t1"])
	}
	if _, ok := types["old"]; ok {
		t.Fatal("expired session must not appear")
	}
}

func TestAdminDelete(t *testing.T) {
	api, r := newAdminRouter(t, "admin", "secret")
	ctx := context.Background()
	if _, err := api.Store.Create(ctx, store.CreateOpts{Slug: "doomed"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/sessions/doomed", nil)
	req.Header.Set("Authorization", basicAuthHeader("admin", "secret"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	// Second delete → 404
	req2 := httptest.NewRequest(http.MethodDelete, "/admin/api/sessions/doomed", nil)
	req2.Header.Set("Authorization", basicAuthHeader("admin", "secret"))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("second delete want 404, got %d", w2.Code)
	}
}

func TestAdminDeleteUnauthorized(t *testing.T) {
	api, r := newAdminRouter(t, "admin", "secret")
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "safe"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/sessions/safe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	// Session must still exist
	if _, err := api.Store.Get(context.Background(), "safe"); err != nil {
		t.Fatalf("session deleted without auth: %v", err)
	}
}
