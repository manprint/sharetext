package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		t.Fatalf("want ErrNotFound, got %v", err)
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
	if _, err := s.Update(ctx, "s1", "hello\n[file:keep:k.txt]\nbye"); err != nil {
		t.Fatal(err)
	}
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

func TestAddFileRespectsMaxFilesPerSession(t *testing.T) {
	s := openTestWithOptions(t, Options{MaxFilesPerSession: 1})
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "quota"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "quota", "a", "a.txt", "text/plain", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "quota", "b", "b.txt", "text/plain", []byte("b")); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("want ErrTooManyFiles, got %v", err)
	}
}

func TestAddFileFilesystemBackend(t *testing.T) {
	s := openTestWithOptions(t, Options{FileBackend: FileBackendFS})
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "sess1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "sess1", "fid1", "notes.txt", "text/plain", []byte("hello fs")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.fsFilePath("sess1", "fid1")); err != nil {
		t.Fatalf("expected file on disk: %v", err)
	}
	var backend string
	var blobLen int
	if err := s.db.QueryRowContext(ctx, `SELECT storage_backend, length(data) FROM files WHERE id = ?`, "fid1").Scan(&backend, &blobLen); err != nil {
		t.Fatal(err)
	}
	if backend != FileBackendFS || blobLen != 0 {
		t.Fatalf("unexpected backend row backend=%q blobLen=%d", backend, blobLen)
	}
	f, err := s.GetFile(ctx, "sess1", "fid1")
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Data) != "hello fs" {
		t.Fatalf("unexpected file data: %q", f.Data)
	}
	if err := s.DeleteFile(ctx, "sess1", "fid1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.fsFilePath("sess1", "fid1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected file removed from disk, got %v", err)
	}
}

func TestFilesystemBackedFileReadableAfterReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fs.db")
	s1, err := OpenWithOptions(dbPath, Options{FileBackend: FileBackendFS})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s1.Create(ctx, CreateOpts{Slug: "room"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.AddFile(ctx, "room", "fid1", "x.txt", "text/plain", []byte("persisted")); err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := OpenWithOptions(dbPath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	f, err := s2.GetFile(ctx, "room", "fid1")
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Data) != "persisted" {
		t.Fatalf("unexpected reopened data: %q", f.Data)
	}
}

func TestUpdateMaintainsFileRefs(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "refs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, "refs", "[file:a:x.txt]\n[file:b:y.txt]"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_refs WHERE session_slug = ?`, "refs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("want 2 refs, got %d", count)
	}
	if _, err := s.Update(ctx, "refs", "[file:b:y.txt]"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_refs WHERE session_slug = ?`, "refs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want 1 ref after update, got %d", count)
	}
	var fileID string
	if err := s.db.QueryRowContext(ctx, `SELECT file_id FROM file_refs WHERE session_slug = ?`, "refs").Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if fileID != "b" {
		t.Fatalf("want remaining ref b, got %q", fileID)
	}
}

func TestBackfillFileRefsOnOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "backfill.db")
	s1, err := OpenWithOptions(dbPath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s1.Create(ctx, CreateOpts{Slug: "legacy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.db.ExecContext(ctx, `UPDATE sessions SET content = ? WHERE slug = ?`, "[file:legacy-id:name.txt]", "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.db.ExecContext(ctx, `DELETE FROM file_refs`); err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := OpenWithOptions(dbPath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var count int
	if err := s2.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_refs WHERE session_slug = ?`, "legacy").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want 1 backfilled ref, got %d", count)
	}
}
