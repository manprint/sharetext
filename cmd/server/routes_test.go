package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"sharetext/internal/store"
)

func TestExistingSlugGuardsSessionPages(t *testing.T) {
	st, err := store.OpenWithOptions(filepath.Join(t.TempDir(), "t.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Create(context.Background(), store.CreateOpts{Slug: "livesession12"}); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Get("/launch/{slug}", func(w http.ResponseWriter, r *http.Request) {
		if slug, ok := existingSlug(st, w, r); ok {
			_, _ = w.Write([]byte(slug))
		}
	})
	for path, want := range map[string]int{"/launch/livesession12": http.StatusOK, "/launch/missing": http.StatusNotFound} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Errorf("%s: got %d want %d", path, w.Code, want)
		}
	}
}
