package handlers

import (
	"context"
	"encoding/json"
	"net/http"
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
	api := &API{Store: st, Hub: NewHub(), Locks: NewLockManager(2 * time.Second), SlugLen: 16}
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

// wsEnvelope is a permissive decoder for either a sessionResp or a typed event
// (lock / lock_denied). Tests use it to distinguish event kinds.
type wsEnvelope struct {
	Type     string        `json:"type,omitempty"`
	Slug     string        `json:"slug,omitempty"`
	Content  string        `json:"content,omitempty"`
	ClientID string        `json:"client_id,omitempty"`
	Lock     *LockSnapshot `json:"lock,omitempty"`
}

func readEnvelope(t *testing.T, ctx context.Context, c *websocket.Conn) wsEnvelope {
	t.Helper()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var e wsEnvelope
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("decode %q: %v", string(data), err)
	}
	return e
}

// readUntil returns the first message whose Type matches one of the expected
// values. Other messages (e.g. interleaved lock events) are discarded.
func readUntil(t *testing.T, ctx context.Context, c *websocket.Conn, want ...string) wsEnvelope {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e := readEnvelope(t, ctx, c)
		for _, w := range want {
			if e.Type == w {
				return e
			}
		}
	}
	t.Fatalf("did not see any of %v before timeout", want)
	return wsEnvelope{}
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

func TestWSInitialPayloadCarriesClientIDAndLock(t *testing.T) {
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "wshello"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wshello"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	init := readJSON(t, ctx, c)
	if init.ClientID == "" {
		t.Fatal("initial WS payload must include a client_id")
	}
	if init.Lock == nil {
		t.Fatal("initial WS payload must include a lock snapshot")
	}
	if init.Lock.Held {
		t.Fatalf("freshly created session must not be locked, got %+v", *init.Lock)
	}
}

func TestWSEditAcquiresLockAndBroadcasts(t *testing.T) {
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "wslk"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c1, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wslk"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()
	c2, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wslk"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()

	h1 := readJSON(t, ctx, c1)
	h2 := readJSON(t, ctx, c2)
	if h1.ClientID == "" || h2.ClientID == "" {
		t.Fatal("both connections must receive client_id")
	}
	if h1.ClientID == h2.ClientID {
		t.Fatal("each WS must mint a unique client_id")
	}

	// c1 edits → server should acquire lock for c1 and broadcast.
	body, _ := json.Marshal(wsMsg{Type: "edit", Content: "first edit"})
	if err := c1.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}

	// c2 should observe both a session update (with the new content) and a lock event.
	sawEdit, sawLockHeld := false, false
	deadline := time.Now().Add(3 * time.Second)
	for !(sawEdit && sawLockHeld) && time.Now().Before(deadline) {
		ev := readEnvelope(t, ctx, c2)
		if ev.Type == "" && ev.Content == "first edit" {
			sawEdit = true
			if ev.Lock == nil || !ev.Lock.Held || ev.Lock.Holder != h1.ClientID {
				t.Fatalf("session broadcast must carry the new lock state, got %+v", ev.Lock)
			}
		}
		if ev.Type == "lock" && ev.Lock != nil && ev.Lock.Held && ev.Lock.Holder == h1.ClientID {
			sawLockHeld = true
		}
	}
	if !sawEdit {
		t.Fatal("c2 did not see the session edit broadcast")
	}
	if !sawLockHeld {
		t.Fatal("c2 did not see the lock event")
	}
}

func TestWSEditDeniedWhenOtherHolds(t *testing.T) {
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "wsden"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c1, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsden"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()
	c2, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsden"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	readJSON(t, ctx, c1)
	readJSON(t, ctx, c2)

	// External lock for someone-else.
	api.Locks.Acquire("wsden", "ghost")

	body, _ := json.Marshal(wsMsg{Type: "edit", Content: "blocked"})
	if err := c2.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}

	denied := readUntil(t, ctx, c2, "lock_denied")
	if denied.Lock == nil || denied.Lock.Holder != "ghost" {
		t.Fatalf("lock_denied must point at the real holder, got %+v", denied.Lock)
	}
	// Content must NOT have been written.
	sess, err := api.Store.Get(context.Background(), "wsden")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Content != "" {
		t.Fatalf("denied edit must not persist content, got %q", sess.Content)
	}
}

func TestWSExplicitLockAcquireBroadcasts(t *testing.T) {
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "wsacq"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c1, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsacq"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()
	c2, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsacq"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	h1 := readJSON(t, ctx, c1)
	readJSON(t, ctx, c2)

	body, _ := json.Marshal(wsMsg{Type: "lock_acquire"})
	if err := c1.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}

	ev := readUntil(t, ctx, c2, "lock")
	if ev.Lock == nil || !ev.Lock.Held || ev.Lock.Holder != h1.ClientID {
		t.Fatalf("expected lock event with holder=%q, got %+v", h1.ClientID, ev.Lock)
	}
}

func TestWSExplicitLockReleaseBroadcasts(t *testing.T) {
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "wsrel"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c1, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsrel"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()
	c2, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsrel"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	readJSON(t, ctx, c1)
	readJSON(t, ctx, c2)

	acq, _ := json.Marshal(wsMsg{Type: "lock_acquire"})
	if err := c1.Write(ctx, websocket.MessageText, acq); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ctx, c2, "lock") // wait for held state

	rel, _ := json.Marshal(wsMsg{Type: "lock_release"})
	if err := c1.Write(ctx, websocket.MessageText, rel); err != nil {
		t.Fatal(err)
	}
	ev := readUntil(t, ctx, c2, "lock")
	if ev.Lock == nil || ev.Lock.Held {
		t.Fatalf("expected free lock after release, got %+v", ev.Lock)
	}
}

func TestWSDisconnectReleasesLock(t *testing.T) {
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "wsdis"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c1, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsdis"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c2, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wsdis"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	readJSON(t, ctx, c1)
	readJSON(t, ctx, c2)

	acq, _ := json.Marshal(wsMsg{Type: "lock_acquire"})
	if err := c1.Write(ctx, websocket.MessageText, acq); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ctx, c2, "lock")

	// Close c1: server defer should release.
	_ = c1.Close(websocket.StatusNormalClosure, "bye")

	ev := readUntil(t, ctx, c2, "lock")
	if ev.Lock == nil || ev.Lock.Held {
		t.Fatalf("expected lock to be released on disconnect, got %+v", ev.Lock)
	}
	if api.Locks.State("wsdis").Held {
		t.Fatal("server-side state must show free")
	}
}

func TestWSLegacyEditMessageStillWorks(t *testing.T) {
	// A message with no "type" but a "content" field is treated as an edit
	// for backward compatibility with older clients.
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "wslegacy"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c1, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wslegacy"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()
	c2, _, err := websocket.Dial(ctx, wsURL(srv.URL, "wslegacy"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	readJSON(t, ctx, c1)
	readJSON(t, ctx, c2)

	body, _ := json.Marshal(wsMsg{Content: "legacy"})
	if err := c1.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	for {
		ev := readEnvelope(t, ctx, c2)
		if ev.Type == "" && ev.Content == "legacy" {
			return
		}
	}
}

func TestWSRejectsForeignOrigin(t *testing.T) {
	srv, api := newWSServer(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "origincheck"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hdr := http.Header{}
	hdr.Set("Origin", "https://evil.example")
	if _, _, err := websocket.Dial(ctx, wsURL(srv.URL, "origincheck"), &websocket.DialOptions{HTTPHeader: hdr}); err == nil {
		t.Fatal("expected dial to fail with foreign origin")
	}
}

func TestWSAllowsConfiguredOrigin(t *testing.T) {
	srv, api := newWSServer(t)
	api.AllowedOrigins = []string{"evil.example"}
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "originallow"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hdr := http.Header{}
	hdr.Set("Origin", "https://evil.example")
	c, _, err := websocket.Dial(ctx, wsURL(srv.URL, "originallow"), &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("dial with allow-listed origin failed: %v", err)
	}
	defer c.CloseNow()
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
