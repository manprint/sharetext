package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"sharetext/internal/store"
)

func TestDownloadContentType(t *testing.T) {
	for in, want := range map[string]string{
		"text/html":                "application/octet-stream",
		"Text/HTML; charset=utf-8": "application/octet-stream",
		"image/svg+xml":            "application/octet-stream",
		"bad;;;":                   "application/octet-stream",
		"image/png":                "image/png",
		"text/plain":               "text/plain",
	} {
		if got := downloadContentType(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestAuthLockout(t *testing.T) {
	l := newAuthLockout(3, time.Minute)
	for i := 0; i < 2; i++ {
		l.fail("1.1.1.1")
	}
	if l.blocked("1.1.1.1") != 0 {
		t.Fatal("locked too early")
	}
	l.fail("1.1.1.1")
	if l.blocked("1.1.1.1") == 0 {
		t.Fatal("not locked")
	}
	if l.blocked("2.2.2.2") != 0 {
		t.Fatal("other ip locked")
	}
	l.reset("1.1.1.1")
	if l.blocked("1.1.1.1") != 0 {
		t.Fatal("reset failed")
	}
	if newAuthLockout(0, 0).blocked("x") != 0 {
		t.Fatal("disabled lockout blocks")
	}
}

func TestRFC5987Escape(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":    "report.pdf",
		"a b.txt":       "a%20b.txt",
		"x:y=z@w,v.txt": "x%3Ay%3Dz%40w%2Cv.txt",
		"perché.txt":    "perch%C3%A9.txt",
		`q"u'o;te`:      "q%22u%27o%3Bte",
	} {
		if got := rfc5987Escape(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestNormalizeUploadMIME(t *testing.T) {
	long := "application/x-" + strings.Repeat("a", 300)
	for in, want := range map[string]string{
		"":                          "application/octet-stream",
		"bad;;;":                    "application/octet-stream",
		"Image/PNG":                 "image/png",
		"text/plain; charset=UTF-8": "text/plain; charset=UTF-8",
		"text/plain; x=" + strings.Repeat("b", 300): "text/plain",
		long: "application/octet-stream",
	} {
		if got := normalizeUploadMIME(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestUpdateRefusesGrowthWhenServerFull(t *testing.T) {
	api, r := newTestAPI(t)
	w := doJSON(t, r, http.MethodPost, "/api/sessions", createReq{Type: "temporary", Minutes: 60})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct{ Slug string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	path := "/api/sessions/" + created.Slug
	if w := doJSON(t, r, http.MethodPut, path, map[string]string{"content": "hello world"}); w.Code != http.StatusOK {
		t.Fatalf("seed: %d", w.Code)
	}
	api.MaxTotalStorageBytes = 5 // already over the limit
	if w := doJSON(t, r, http.MethodPut, path, map[string]string{"content": "hello world, bigger"}); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("growth: want 507, got %d", w.Code)
	}
	if w := doJSON(t, r, http.MethodPut, path, map[string]string{"content": "hi"}); w.Code != http.StatusOK {
		t.Fatalf("shrink: want 200, got %d", w.Code)
	}
}

func TestUpdateContentCountsGrowthOnce(t *testing.T) {
	api, _ := newTestAPI(t)
	ctx := context.Background()
	if _, err := api.Store.Create(ctx, store.CreateOpts{Slug: "quota"}); err != nil {
		t.Fatal(err)
	}
	api.MaxTotalStorageBytes = 1 << 20
	if _, err := api.updateContent(ctx, "quota", "0123456789"); err != nil {
		t.Fatal(err)
	}
	if got := api.limits().added.Load(); got != 10 {
		t.Fatalf("growth counted %d bytes, want 10", got)
	}
	api.MaxTotalStorageBytes = 15
	if _, err := api.updateContent(ctx, "quota", "0123456789-bigger-than-the-limit"); !errors.Is(err, store.ErrStorageFull) {
		t.Fatalf("want ErrStorageFull, got %v", err)
	}
	if _, err := api.updateContent(ctx, "missing", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if got := api.limits().added.Load(); got != 10 {
		t.Fatalf("refused updates must not be counted, got %d", got)
	}
}
