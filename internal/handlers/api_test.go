package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"sharetext/internal/store"
)

func newTestAPI(t *testing.T) (*API, *chi.Mux) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := &API{Store: st, Hub: NewHub(), SlugLen: 16}
	r := chi.NewRouter()
	r.Post("/api/sessions", api.CreateSession)
	r.Get("/api/sessions/{slug}", api.GetSession)
	r.Put("/api/sessions/{slug}", api.UpdateSession)
	return api, r
}

func doJSON(t *testing.T, r *chi.Mux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCreatePersistentRequiresName(t *testing.T) {
	_, r := newTestAPI(t)
	w := doJSON(t, r, http.MethodPost, "/api/sessions", createReq{Type: "persistent"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestCreatePersistentInvalidName(t *testing.T) {
	_, r := newTestAPI(t)
	cases := []string{"with space", "dot.name", "slash/name", "èaccent", strings.Repeat("a", 33)}
	for _, n := range cases {
		w := doJSON(t, r, http.MethodPost, "/api/sessions", createReq{Type: "persistent", Name: n})
		if w.Code != http.StatusBadRequest {
			t.Errorf("name %q want 400, got %d", n, w.Code)
		}
	}
}

func TestCreatePersistentValid(t *testing.T) {
	_, r := newTestAPI(t)
	w := doJSON(t, r, http.MethodPost, "/api/sessions", createReq{Type: "persistent", Name: "team_one-42"})
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp createResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.Slug, "team_one-42-") {
		t.Fatalf("slug must start with name, got %s", resp.Slug)
	}
	if resp.ExpiresAt != nil {
		t.Fatalf("persistent must not have expires_at")
	}
	if resp.Name != "team_one-42" {
		t.Fatalf("want name echoed, got %q", resp.Name)
	}
}

func TestCreateTemporaryRequiresMinutes(t *testing.T) {
	_, r := newTestAPI(t)
	w := doJSON(t, r, http.MethodPost, "/api/sessions", createReq{Type: "temporary"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestCreateTemporaryInvalidMinutes(t *testing.T) {
	_, r := newTestAPI(t)
	for _, m := range []int{0, -5, MaxSessionMinutes + 1} {
		w := doJSON(t, r, http.MethodPost, "/api/sessions", createReq{Type: "temporary", Minutes: m})
		if w.Code != http.StatusBadRequest {
			t.Errorf("minutes %d want 400, got %d", m, w.Code)
		}
	}
}

func TestCreateTemporaryValid(t *testing.T) {
	_, r := newTestAPI(t)
	w := doJSON(t, r, http.MethodPost, "/api/sessions", createReq{Type: "temporary", Minutes: 30})
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp createResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ExpiresAt == nil {
		t.Fatal("temporary must have expires_at")
	}
	delta := time.Until(*resp.ExpiresAt)
	if delta < 29*time.Minute || delta > 31*time.Minute {
		t.Fatalf("expires_at out of range: %v", delta)
	}
	if resp.Name != "" {
		t.Fatalf("temporary must have empty name")
	}
}

func TestCreateUnknownType(t *testing.T) {
	_, r := newTestAPI(t)
	w := doJSON(t, r, http.MethodPost, "/api/sessions", createReq{Type: "weird"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestGetSession(t *testing.T) {
	api, r := newTestAPI(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "fixed1"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/fixed1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp sessionResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Slug != "fixed1" {
		t.Fatalf("want fixed1, got %s", resp.Slug)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	_, r := newTestAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nope", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestGetSessionExpired(t *testing.T) {
	api, r := newTestAPI(t)
	past := time.Now().Add(-time.Minute).UTC()
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "expd", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/expd", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("want 410, got %d", w.Code)
	}
}

func TestUpdateSession(t *testing.T) {
	api, r := newTestAPI(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "upd1"}); err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, r, http.MethodPut, "/api/sessions/upd1", updateReq{Content: "hello"})
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp sessionResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" {
		t.Fatalf("want hello, got %q", resp.Content)
	}
}

func TestUpdateBadJSON(t *testing.T) {
	api, r := newTestAPI(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "j1"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/j1", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestUpdateNotFound(t *testing.T) {
	_, r := newTestAPI(t)
	w := doJSON(t, r, http.MethodPut, "/api/sessions/missing", updateReq{Content: "x"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestUpdateExpiredReturns410(t *testing.T) {
	api, r := newTestAPI(t)
	past := time.Now().Add(-time.Minute).UTC()
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "old", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, r, http.MethodPut, "/api/sessions/old", updateReq{Content: "x"})
	if w.Code != http.StatusGone {
		t.Fatalf("want 410, got %d", w.Code)
	}
}
