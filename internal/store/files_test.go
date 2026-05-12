package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAddAndGetFile(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "sess1"}); err != nil {
		t.Fatal(err)
	}
	data := []byte("hello bytes")
	sum, err := s.AddFile(ctx, "sess1", "fid1", "notes.txt", "text/plain", data)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Size != int64(len(data)) {
		t.Fatalf("size mismatch: %d", sum.Size)
	}

	f, err := s.GetFile(ctx, "sess1", "fid1")
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Data) != "hello bytes" || f.Filename != "notes.txt" || f.MIME != "text/plain" {
		t.Fatalf("unexpected file: %+v", f)
	}
}

func TestAddFileMissingSession(t *testing.T) {
	s := openTest(t)
	_, err := s.AddFile(context.Background(), "ghost", "fid", "x.txt", "text/plain", []byte("x"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAddFileExpiredSession(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).UTC()
	if _, err := s.Create(ctx, CreateOpts{Slug: "exp", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddFile(ctx, "exp", "fid", "x.txt", "text/plain", []byte("x"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound (Exists treats expired as missing), got %v", err)
	}
}

func TestListFiles(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "sess"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "sess", "a", "a.txt", "text/plain", []byte("a")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := s.AddFile(ctx, "sess", "b", "b.bin", "application/octet-stream", []byte("bb")); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListFiles(ctx, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}
	if list[0].ID != "a" || list[1].ID != "b" {
		t.Fatalf("wrong order: %+v", list)
	}
}

func TestDeleteFile(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "s", "id", "x.txt", "text/plain", []byte("y")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFile(ctx, "s", "id"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFile(ctx, "s", "id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFileCascadeOnSessionDelete(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "doomed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "doomed", "fid", "a.txt", "text/plain", []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "doomed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFile(ctx, "doomed", "fid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("file should be gone after session delete: %v", err)
	}
}

func TestReferencedFileIDs(t *testing.T) {
	cases := []struct {
		content string
		want    []string
	}{
		{"", nil},
		{"plain text", nil},
		{"[file:abc:name.txt]", []string{"abc"}},
		{"line one\n[file:id-1:a.txt]\nline two\n[file:id_2:b.bin]\n", []string{"id-1", "id_2"}},
		{"inline [file:inl:foo.txt] still counts", []string{"inl"}},
		{"[file:dup:a.txt]\n[file:dup:a.txt]", []string{"dup"}},
	}
	for _, c := range cases {
		got := ReferencedFileIDs(c.content)
		if len(got) != len(c.want) {
			t.Errorf("ReferencedFileIDs(%q) len=%d, want %d", c.content, len(got), len(c.want))
			continue
		}
		for _, id := range c.want {
			if _, ok := got[id]; !ok {
				t.Errorf("ReferencedFileIDs(%q) missing %q", c.content, id)
			}
		}
	}
}

func TestDeleteOrphanFilesUnreferenced(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "s1", "keep", "k.txt", "text/plain", []byte("k")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "s1", "drop", "d.txt", "text/plain", []byte("d")); err != nil {
		t.Fatal(err)
	}
	// Only one marker references 'keep'.
	if _, err := s.Update(ctx, "s1", "hello\n[file:keep:k.txt]\nbye"); err != nil {
		t.Fatal(err)
	}
	// Move 'drop' file's created_at into the past so grace doesn't protect it.
	if _, err := s.db.ExecContext(ctx, `UPDATE files SET created_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).Unix(), "drop"); err != nil {
		t.Fatal(err)
	}

	n, err := s.DeleteOrphanFiles(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 deleted, got %d", n)
	}
	if _, err := s.GetFile(ctx, "s1", "keep"); err != nil {
		t.Fatalf("keep should still exist: %v", err)
	}
	if _, err := s.GetFile(ctx, "s1", "drop"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("drop should be gone: %v", err)
	}
}

func TestDeleteOrphanFilesGraceProtectsFreshUploads(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "s1"}); err != nil {
		t.Fatal(err)
	}
	// Just uploaded, no marker yet in content. Should be preserved by grace.
	if _, err := s.AddFile(ctx, "s1", "fresh", "f.txt", "text/plain", []byte("f")); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteOrphanFiles(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("fresh upload must be protected by grace; got n=%d", n)
	}
	if _, err := s.GetFile(ctx, "s1", "fresh"); err != nil {
		t.Fatalf("fresh should still exist: %v", err)
	}
}

func TestDeleteOrphanFilesAfterSessionGoneFallback(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "alive"}); err != nil {
		t.Fatal(err)
	}
	// Insert a file row pointing at a non-existent session by temporarily
	// disabling FK enforcement; simulates a corrupt-state recovery path.
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO files (id, session_slug, filename, mime, size, data, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"ghost", "missing", "g.txt", "text/plain", 1, []byte("g"), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}

	n, err := s.DeleteOrphanFiles(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 deleted (ghost), got %d", n)
	}
}

func TestFileCascadeOnExpiredCleanup(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).UTC()
	if _, err := s.Create(ctx, CreateOpts{Slug: "expired-with-files", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	// Inserting on expired session via AddFile fails (Exists returns false),
	// so insert directly via the underlying handle to verify cascade.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO files (id, session_slug, filename, mime, size, data, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"orphan", "expired-with-files", "z.txt", "text/plain", 1, []byte("z"), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE session_slug = ?`, "expired-with-files")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected files cascade-deleted, found %d", n)
	}
}
