package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"sharetext/internal/store"
)

func newFilesRouter(t *testing.T) (*API, *chi.Mux) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := &API{Store: st, Hub: NewHub(), SlugLen: 16}
	r := chi.NewRouter()
	r.Post("/api/sessions/{slug}/files", api.UploadFile)
	r.Get("/api/sessions/{slug}/files", api.ListFiles)
	r.Get("/api/sessions/{slug}/files/{id}", api.DownloadFile)
	r.Get("/api/sessions/{slug}/bundle", api.Bundle)
	return api, r
}

func doUpload(t *testing.T, r *chi.Mux, slug, filename, mime string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	if mime != "" {
		hdr["Content-Type"] = []string{mime}
	}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(body)
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+slug+"/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestUploadFile(t *testing.T) {
	api, r := newFilesRouter(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "u1"}); err != nil {
		t.Fatal(err)
	}
	w := doUpload(t, r, "u1", "notes.txt", "text/plain", []byte("hello"))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp fileResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Size != 5 || resp.Filename != "notes.txt" {
		t.Fatalf("unexpected resp: %+v", resp)
	}
	if !strings.HasPrefix(resp.Marker, "[file:") || !strings.Contains(resp.Marker, "notes.txt") {
		t.Fatalf("marker malformed: %q", resp.Marker)
	}
	if !strings.HasPrefix(resp.URL, "/api/sessions/u1/files/") {
		t.Fatalf("url malformed: %q", resp.URL)
	}
}

func TestUploadMissingFile(t *testing.T) {
	api, r := newFilesRouter(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "u2"}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/u2/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestUploadUnknownSession(t *testing.T) {
	_, r := newFilesRouter(t)
	w := doUpload(t, r, "ghost", "a.txt", "text/plain", []byte("x"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestUploadUnknownSessionReturns404BeforeSizeCheck(t *testing.T) {
	_, r := newFilesRouter(t)
	old := MaxFileSize
	MaxFileSize = 16
	t.Cleanup(func() { MaxFileSize = old })
	w := doUpload(t, r, "ghost", "big.bin", "application/octet-stream", []byte(strings.Repeat("x", 64)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestUploadOversize(t *testing.T) {
	api, r := newFilesRouter(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "u3"}); err != nil {
		t.Fatal(err)
	}
	old := MaxFileSize
	MaxFileSize = 16
	t.Cleanup(func() { MaxFileSize = old })

	w := doUpload(t, r, "u3", "big.bin", "application/octet-stream", []byte(strings.Repeat("x", 64)))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", w.Code)
	}
}

func TestDownloadFile(t *testing.T) {
	api, r := newFilesRouter(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "d1"}); err != nil {
		t.Fatal(err)
	}
	body := []byte("xyz")
	w := doUpload(t, r, "d1", "a.bin", "application/octet-stream", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d %s", w.Code, w.Body.String())
	}
	var up fileResp
	if err := json.NewDecoder(w.Body).Decode(&up); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/d1/files/"+up.ID, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("download want 200, got %d", w2.Code)
	}
	if !bytes.Equal(w2.Body.Bytes(), body) {
		t.Fatalf("body mismatch: %q vs %q", w2.Body.Bytes(), body)
	}
	if cd := w2.Header().Get("Content-Disposition"); !strings.Contains(cd, `attachment; filename="a.bin"`) {
		t.Fatalf("bad Content-Disposition: %s", cd)
	}
}

func TestDownloadFileNotFound(t *testing.T) {
	api, r := newFilesRouter(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "d2"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/d2/files/missing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestListFiles(t *testing.T) {
	api, r := newFilesRouter(t)
	if _, err := api.Store.Create(context.Background(), store.CreateOpts{Slug: "l1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Store.AddFile(context.Background(), "l1", "id1", "a.txt", "text/plain", []byte("a")); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/l1/files", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Files []fileResp `json:"files"`
		Count int        `json:"count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count != 1 || resp.Files[0].ID != "id1" {
		t.Fatalf("unexpected list: %+v", resp)
	}
}

func TestBundleZip(t *testing.T) {
	api, r := newFilesRouter(t)
	ctx := context.Background()
	if _, err := api.Store.Create(ctx, store.CreateOpts{Slug: "b1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Store.Update(ctx, "b1", "hello world"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Store.AddFile(ctx, "b1", "fa", "a.txt", "text/plain", []byte("AA")); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Store.AddFile(ctx, "b1", "fb", "a.txt", "text/plain", []byte("BB")); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/b1/bundle", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("bad content type: %s", ct)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		buf, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = buf
	}
	if string(names["b1.txt"]) != "hello world" {
		t.Fatalf("missing or wrong b1.txt: %v", names)
	}
	if string(names["files/a.txt"]) == "" || string(names["files/a-2.txt"]) == "" {
		t.Fatalf("duplicate filename not deduped: %v", names)
	}
}
