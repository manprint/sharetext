package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	sweepID1 = "ABCDEFGHJKMN"
	sweepID2 = "PQRSTUVWXYZa"
	sweepID3 = "bcdefghijkmn"
	sweepID4 = "pqrstuvwxyz2"
	sweepID5 = "3456789ABCDE"
)

type sweepFixture struct {
	t    *testing.T
	root string
	old  time.Time
}

func (f sweepFixture) path(rel string) string {
	return filepath.Join(f.root, filepath.FromSlash(rel))
}

func (f sweepFixture) file(rel string, stale bool) {
	f.t.Helper()
	p := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if stale {
		f.age(rel)
	}
}

func (f sweepFixture) dir(rel string) {
	f.t.Helper()
	if err := os.MkdirAll(f.path(rel), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// age backdates rel; directories must be aged after their entries exist,
// since creating an entry refreshes the directory's mtime.
func (f sweepFixture) age(rel string) {
	f.t.Helper()
	if err := os.Chtimes(f.path(rel), f.old, f.old); err != nil {
		f.t.Fatal(err)
	}
}

func (f sweepFixture) exists(rel string) bool {
	_, err := os.Lstat(f.path(rel))
	return err == nil
}

func TestSweepFSOrphans(t *testing.T) {
	s := openTestWithOptions(t, Options{FileBackend: FileBackendFS})
	ctx := context.Background()
	now := time.Now()
	f := sweepFixture{t: t, root: s.opts.FileStorageDir, old: now.Add(-time.Hour)}
	outside := t.TempDir()

	// Live session with a referenced file and assorted strays.
	if _, err := s.Create(ctx, CreateOpts{Slug: "live"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "live", sweepID1, "kept.txt", "text/plain", []byte("kept")); err != nil {
		t.Fatal(err)
	}
	f.age("live/" + sweepID1)
	f.file("live/"+sweepID2, true)             // stale, unreferenced: removed
	f.file("live/"+sweepID3+"-4242.tmp", true) // stale temp: removed
	f.file("live/"+sweepID4, false)            // fresh, maybe mid-upload: kept
	f.file("live/notes.txt", true)             // unknown name: kept
	if err := os.Symlink(filepath.Join(outside, "target"), f.path("live/"+sweepID5)); err != nil {
		t.Fatal(err)
	}

	// Expired session not deleted yet: still live.
	future := now.Add(time.Hour)
	if _, err := s.Create(ctx, CreateOpts{Slug: "expiring", ExpiresAt: &future}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "expiring", sweepID5, "x.txt", "text/plain", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE slug = ?`, now.Add(-time.Minute).Unix(), "expiring"); err != nil {
		t.Fatal(err)
	}
	f.age("expiring/" + sweepID5)
	f.age("expiring")

	// Orphans the sweep removes.
	f.file("stale-orphan/"+sweepID1, true)
	f.file("stale-orphan/"+sweepID2+"-17.tmp", true)
	f.age("stale-orphan")
	f.dir("empty-orphan")
	f.age("empty-orphan")

	// Orphans too young to remove.
	f.file("fresh-orphan/"+sweepID1, false)
	f.file("fresh-dir-orphan/"+sweepID1, true)
	f.dir("fresh-empty")

	// Orphans holding something unexpected: kept and reported.
	f.file("backups/notes.txt", true)
	f.age("backups")
	f.file("mixed-orphan/"+sweepID1, true)
	f.file("mixed-orphan/README", true)
	f.age("mixed-orphan")
	f.file("subdir-orphan/"+sweepID1+"/"+sweepID2, true)
	f.age("subdir-orphan/" + sweepID1)
	f.age("subdir-orphan")
	if err := os.WriteFile(filepath.Join(outside, "target"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.dir("symlink-orphan")
	if err := os.Symlink(filepath.Join(outside, "target"), f.path("symlink-orphan/"+sweepID1)); err != nil {
		t.Fatal(err)
	}
	f.age("symlink-orphan")

	// Root entries that are not session directories: ignored.
	f.file("bad.name/"+sweepID1, true)
	f.age("bad.name")
	f.file(sweepID2, true)
	if err := os.MkdirAll(filepath.Join(outside, "linked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "linked", sweepID1), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(outside, "linked", sweepID1), f.old, f.old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "linked"), f.path("linked-orphan")); err != nil {
		t.Fatal(err)
	}

	res, err := s.SweepFSOrphans(ctx, now, DefaultFSSweepGrace)
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovedDirs != 2 || res.RemovedFiles != 4 {
		t.Fatalf("want 2 dirs and 4 files removed, got %+v", res)
	}
	wantSkipped := []string{"backups", "mixed-orphan", "subdir-orphan", "symlink-orphan"}
	if fmt.Sprint(res.SkippedDirs) != fmt.Sprint(wantSkipped) || res.SkippedCount != len(wantSkipped) {
		t.Fatalf("skipped = %v (%d), want %v", res.SkippedDirs, res.SkippedCount, wantSkipped)
	}

	gone := []string{
		"live/" + sweepID2,
		"live/" + sweepID3 + "-4242.tmp",
		"stale-orphan",
		"empty-orphan",
	}
	for _, rel := range gone {
		if f.exists(rel) {
			t.Errorf("%s should have been removed", rel)
		}
	}
	kept := []string{
		"live/" + sweepID1,
		"live/" + sweepID4,
		"live/notes.txt",
		"live/" + sweepID5,
		"expiring/" + sweepID5,
		"fresh-orphan/" + sweepID1,
		"fresh-dir-orphan/" + sweepID1,
		"fresh-empty",
		"backups/notes.txt",
		"mixed-orphan/" + sweepID1,
		"mixed-orphan/README",
		"subdir-orphan/" + sweepID1 + "/" + sweepID2,
		"symlink-orphan/" + sweepID1,
		"bad.name/" + sweepID1,
		sweepID2,
		"linked-orphan",
	}
	for _, rel := range kept {
		if !f.exists(rel) {
			t.Errorf("%s should have been kept", rel)
		}
	}
	for _, p := range []string{filepath.Join(outside, "target"), filepath.Join(outside, "linked", sweepID1)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("symlink target %s must be untouched: %v", p, err)
		}
	}
	if _, err := s.GetFile(ctx, "live", sweepID1); err != nil {
		t.Fatalf("referenced file must stay readable: %v", err)
	}

	again, err := s.SweepFSOrphans(ctx, now, DefaultFSSweepGrace)
	if err != nil {
		t.Fatal(err)
	}
	if again.RemovedDirs != 0 || again.RemovedFiles != 0 || again.SkippedCount != len(wantSkipped) {
		t.Fatalf("second sweep must be a no-op, got %+v", again)
	}
}

// Files written while the backend was "fs" stay on disk after a switch to
// "db", so the sweep runs whatever the current backend is.
func TestSweepFSOrphansRunsWithDBBackend(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "files")
	s, err := OpenWithOptions(filepath.Join(dir, "test.db"), Options{FileBackend: FileBackendDB, FileStorageDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now()

	res, err := s.SweepFSOrphans(ctx, now, DefaultFSSweepGrace)
	if err != nil || res.RemovedDirs != 0 || res.RemovedFiles != 0 {
		t.Fatalf("missing root must be a no-op, got %+v, %v", res, err)
	}

	f := sweepFixture{t: t, root: root, old: now.Add(-time.Hour)}
	f.file("old-session/"+sweepID1, true)
	f.age("old-session")
	res, err = s.SweepFSOrphans(ctx, now, DefaultFSSweepGrace)
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovedDirs != 1 || res.RemovedFiles != 1 || f.exists("old-session") {
		t.Fatalf("want the stale orphan removed, got %+v", res)
	}
}

// A session created under an orphan's name after the snapshot keeps the
// directory, which its uploads are about to reuse.
func TestSweepFSOrphansRechecksSessionBeforeRemoving(t *testing.T) {
	s := openTestWithOptions(t, Options{FileBackend: FileBackendFS})
	ctx := context.Background()
	now := time.Now()
	f := sweepFixture{t: t, root: s.opts.FileStorageDir, old: now.Add(-time.Hour)}
	f.file("reborn/"+sweepID1, true)
	f.age("reborn")
	afterFSSweepSnapshot = func() {
		if _, err := s.Create(ctx, CreateOpts{Slug: "reborn"}); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterFSSweepSnapshot = nil })
	res, err := s.SweepFSOrphans(ctx, now, DefaultFSSweepGrace)
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovedDirs != 0 || res.RemovedFiles != 0 || !f.exists("reborn/"+sweepID1) {
		t.Fatalf("directory of a session created meanwhile must be kept, got %+v", res)
	}
}

func TestSweepFSOrphansCapsSkippedDirs(t *testing.T) {
	s := openTestWithOptions(t, Options{FileBackend: FileBackendFS})
	now := time.Now()
	f := sweepFixture{t: t, root: s.opts.FileStorageDir, old: now.Add(-time.Hour)}
	for i := 0; i < maxReportedSkippedDirs+5; i++ {
		d := fmt.Sprintf("foreign-%02d", i)
		f.file(d+"/keep.txt", true)
		f.age(d)
	}
	res, err := s.SweepFSOrphans(context.Background(), now, DefaultFSSweepGrace)
	if err != nil {
		t.Fatal(err)
	}
	if res.SkippedCount != maxReportedSkippedDirs+5 || len(res.SkippedDirs) != maxReportedSkippedDirs {
		t.Fatalf("want %d skipped, %d named; got %d, %d", maxReportedSkippedDirs+5, maxReportedSkippedDirs, res.SkippedCount, len(res.SkippedDirs))
	}
}

func TestSweepFSNamePatterns(t *testing.T) {
	cases := []struct {
		name     string
		id, temp bool
	}{
		{name: sweepID1, id: true},
		{name: "ABCDEFGHJKM", id: false},   // too short
		{name: "ABCDEFGHJKMNP", id: false}, // too long
		{name: "ABCDEFGHJKMO", id: false},  // O is not in the alphabet
		{name: "abcdefghijk0", id: false},  // neither is 0
		{name: "ABCDEFGHJK.N", id: false},
		{name: sweepID1 + "-123.tmp", temp: true},
		{name: sweepID1 + "-.tmp"},
		{name: sweepID1 + "-12a.tmp"},
		{name: sweepID1 + "-123.tmp.bak"},
		{name: "ABCDEFGHJKM-123.tmp"},
		{name: sweepID1 + "-1-2.tmp"},
		{name: "notes.tmp"},
	}
	for _, tc := range cases {
		if got := isFSFileID(tc.name); got != tc.id {
			t.Errorf("isFSFileID(%q) = %v, want %v", tc.name, got, tc.id)
		}
		if got := isFSTempName(tc.name); got != tc.temp {
			t.Errorf("isFSTempName(%q) = %v, want %v", tc.name, got, tc.temp)
		}
	}
}
