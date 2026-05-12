package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGet(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "abc123"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "abc123" || got.Content != "" || got.Name != "" || got.ExpiresAt != nil {
		t.Fatalf("unexpected session: %+v", got)
	}
}

func TestCreateWithName(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "myteam-abc", Name: "myteam"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "myteam-abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "myteam" {
		t.Fatalf("want name myteam, got %q", got.Name)
	}
}

func TestCreateWithExpiry(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).UTC()
	if _, err := s.Create(ctx, CreateOpts{Slug: "tmp", ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "tmp")
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpiresAt == nil {
		t.Fatal("want expires_at set")
	}
	if got.ExpiresAt.Unix() != exp.Unix() {
		t.Fatalf("want exp %v, got %v", exp.Unix(), got.ExpiresAt.Unix())
	}
}

func TestGetNotFound(t *testing.T) {
	s := openTest(t)
	_, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGetExpired(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	exp := time.Now().Add(-time.Minute).UTC()
	if _, err := s.Create(ctx, CreateOpts{Slug: "old", ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get(ctx, "old")
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestUpdate(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "slug1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, "slug1", "hello"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "slug1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello" {
		t.Fatalf("want hello, got %q", got.Content)
	}
}

func TestUpdateExpired(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	exp := time.Now().Add(-time.Minute).UTC()
	if _, err := s.Create(ctx, CreateOpts{Slug: "old", ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Update(ctx, "old", "x")
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := openTest(t)
	_, err := s.Update(context.Background(), "nope", "x")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDuplicateSlug(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "dup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateOpts{Slug: "dup"}); err == nil {
		t.Fatal("want error on duplicate slug")
	}
}

func TestConcurrentUpdates(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "race"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Update(ctx, "race", "v"); err != nil {
				t.Errorf("update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestExists(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "yes"}); err != nil {
		t.Fatal(err)
	}
	ok, err := s.Exists(ctx, "yes")
	if err != nil || !ok {
		t.Fatalf("want exists, got ok=%v err=%v", ok, err)
	}
	ok, err = s.Exists(ctx, "no")
	if err != nil || ok {
		t.Fatalf("want not exists, got ok=%v err=%v", ok, err)
	}
}

func TestExistsExpired(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	exp := time.Now().Add(-time.Minute).UTC()
	if _, err := s.Create(ctx, CreateOpts{Slug: "exp", ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	ok, err := s.Exists(ctx, "exp")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expired session should not exist")
	}
}

func TestDeleteExpired(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).UTC()
	future := time.Now().Add(time.Hour).UTC()
	if _, err := s.Create(ctx, CreateOpts{Slug: "old1", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateOpts{Slug: "old2", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateOpts{Slug: "keep-tmp", ExpiresAt: &future}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateOpts{Slug: "keep-persist"}); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("want 2 deleted, got %d", n)
	}
	// Persistent and future-tmp must remain
	for _, sl := range []string{"keep-tmp", "keep-persist"} {
		if _, err := s.Get(ctx, sl); err != nil {
			t.Fatalf("%s should still exist: %v", sl, err)
		}
	}
	// Expired rows really gone (hard delete)
	for _, sl := range []string{"old1", "old2"} {
		_, err := s.Get(ctx, sl)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s should be ErrNotFound, got %v", sl, err)
		}
	}
}

func TestListActive(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).UTC()
	future := time.Now().Add(time.Hour).UTC()

	if _, err := s.Create(ctx, CreateOpts{Slug: "persist1", Name: "team"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, "persist1", "hello world"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateOpts{Slug: "tmp-live", ExpiresAt: &future}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateOpts{Slug: "tmp-dead", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 active, got %d: %+v", len(list), list)
	}
	slugs := map[string]SessionSummary{}
	for _, it := range list {
		slugs[it.Slug] = it
	}
	if _, ok := slugs["tmp-dead"]; ok {
		t.Fatal("expired session should not appear in ListActive")
	}
	if s := slugs["persist1"]; s.Name != "team" || s.ContentSize != len("hello world") || s.ExpiresAt != nil {
		t.Fatalf("persistent summary wrong: %+v", s)
	}
	if s := slugs["tmp-live"]; s.ExpiresAt == nil {
		t.Fatalf("temporary summary should have expires_at: %+v", s)
	}
}

func TestListActiveOrdering(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, sl := range []string{"first", "second", "third"} {
		if _, err := s.Create(ctx, CreateOpts{Slug: sl}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond) // ensure distinct unix-second created_at
	}
	list, err := s.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3, got %d", len(list))
	}
	if list[0].Slug != "third" || list[2].Slug != "first" {
		t.Fatalf("expected newest first: %+v", list)
	}
}

func TestDelete(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
