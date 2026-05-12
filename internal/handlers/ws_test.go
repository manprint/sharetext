package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"sharetext/internal/store"
)

func newWSServer(t *testing.T) (*httptest.Server, *API) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := &API{Store: st, Hub: NewHub(), SlugLen: 16}
	r := chi.NewRouter()
	r.Get("/ws/{slug}", api.WS)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, api
}

func wsURL(httpURL, slug string) string {
	return strings.Replace(httpURL, "http://", "ws://", 1) + "/ws/" + slug
}

func readJSON(t *testing.T, ctx context.Context, c *websocket.Conn) sessionResp {
	t.Helper()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var r sessionResp
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWSBroadcastBetweenClients(t *testing.T) {
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "wsroom"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c1, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsroom"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()

	c2, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsroom"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()

	readJSON(t, ctx, c1)
	readJSON(t, ctx, c2)

	body, _ := json.Marshal(wsMsg{Content: "broadcast me"})
	if err := c1.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}

	got := readJSON(t, ctx, c2)
	if got.Content != "broadcast me" {
		t.Fatalf("want 'broadcast me', got %q", got.Content)
	}
}

func TestWSUnknownSlug(t *testing.T) {
	srv, _ := newWSServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := websocket.Dial(ctx, wsURL(srv.URL, "ghost"), nil)
	if err == nil {
		t.Fatal("want dial error for missing slug")
	}
}

func TestWSRejectsOversizedMessage(t *testing.T) {
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "bigws"}); err != nil {
		t.Fatal(err)
	}
	old := MaxContentSize
	MaxContentSize = 5
	t.Cleanup(func() { MaxContentSize = old })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(srv.URL, "bigws"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	readJSON(t, ctx, c)
	body, _ := json.Marshal(wsMsg{Content: "123456"})
	if err := c.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Read(ctx)
	if err == nil {
		t.Fatal("want close error after oversized payload")
	}
	if status := websocket.CloseStatus(err); status != websocket.StatusMessageTooBig {
		t.Fatalf("want close status %d, got %d (err=%v)", websocket.StatusMessageTooBig, status, err)
	}
}
