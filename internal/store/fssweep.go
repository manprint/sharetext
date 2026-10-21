package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"sharetext/internal/session"
)

// DefaultFSSweepGrace is how old an attachment file or directory must be
// before SweepFSOrphans may remove it.
const DefaultFSSweepGrace = 15 * time.Minute

const maxReportedSkippedDirs = 20

var fsSessionDirRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// afterFSSweepSnapshot lets tests act between the snapshot and the sweep.
var afterFSSweepSnapshot func()

// FSSweepResult reports what SweepFSOrphans did.
type FSSweepResult struct {
	RemovedDirs  int
	RemovedFiles int
	// SkippedDirs names up to 20 orphan directories left in place because
	// they hold something the sweep does not recognise; SkippedCount counts
	// all of them.
	SkippedDirs  []string
	SkippedCount int
}

func (r *FSSweepResult) skip(slug string) {
	r.SkippedCount++
	if len(r.SkippedDirs) < maxReportedSkippedDirs {
		r.SkippedDirs = append(r.SkippedDirs, slug)
	}
}

// SweepFSOrphans removes attachment files no session owns any more: session
// directories under FileStorageDir whose session row is gone (older versions
// could leave them behind), and unreferenced or temporary files inside the
// directories of live sessions. It runs whatever FileBackend is, since files
// written while the backend was "fs" stay on disk after a switch.
//
// Only what writeFSFile creates is ever removed: regular files named like a
// file ID (FileIDLen characters of session.Alphabet) or like its temporary
// files (<id>-<digits>.tmp). An orphan directory holding anything else is left
// alone as a whole, symlinks are never followed, and directories are removed
// with os.Remove once emptied, never recursively, so a FILE_STORAGE_DIR that
// points at a shared directory cannot lose unrelated data.
//
// The sessions and files tables are read once up front. Anything modified
// less than grace ago is kept, which covers uploads and sessions that are
// newer than that snapshot: writeFSFile writes a file right before AddFile
// inserts its row, and writing into a directory refreshes its mtime.
func (s *Store) SweepFSOrphans(ctx context.Context, now time.Time, grace time.Duration) (FSSweepResult, error) {
	var res FSSweepResult
	root := s.opts.FileStorageDir
	if root == "" {
		return res, nil
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	live, refs, err := s.fsSweepSnapshot(ctx)
	if err != nil {
		return res, err
	}
	if afterFSSweepSnapshot != nil {
		afterFSSweepSnapshot()
	}
	cutoff := now.Add(-grace)
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		slug := e.Name()
		if !e.IsDir() || !fsSessionDirRe.MatchString(slug) {
			continue
		}
		dir := filepath.Join(root, slug)
		if _, ok := live[slug]; ok {
			sweepLiveFSDir(&res, dir, refs[slug], cutoff)
			continue
		}
		if err := s.sweepOrphanFSDir(ctx, &res, dir, slug, cutoff); err != nil {
			return res, err
		}
	}
	return res, nil
}

// fsSweepSnapshot returns every session slug (expired sessions not yet
// deleted included) and, per slug, the IDs of its files stored on disk.
func (s *Store) fsSweepSnapshot(ctx context.Context) (map[string]struct{}, map[string]map[string]struct{}, error) {
	live := make(map[string]struct{})
	rows, err := s.db.QueryContext(ctx, `SELECT slug FROM sessions`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			return nil, nil, err
		}
		live[slug] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	refs := make(map[string]map[string]struct{})
	rows, err = s.db.QueryContext(ctx, `SELECT session_slug, id FROM files WHERE storage_backend = ?`, FileBackendFS)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var slug, id string
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, nil, err
		}
		if refs[slug] == nil {
			refs[slug] = make(map[string]struct{})
		}
		refs[slug][id] = struct{}{}
	}
	return live, refs, rows.Err()
}

// sweepLiveFSDir removes the stale files of a live session that no file row
// references, plus temporary files an interrupted upload left behind.
func sweepLiveFSDir(res *FSSweepResult, dir string, refs map[string]struct{}, cutoff time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() {
			continue
		}
		if isFSFileID(name) {
			if _, ok := refs[name]; ok {
				continue
			}
		} else if !isFSTempName(name) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			res.RemovedFiles++
		}
	}
}

// sweepOrphanFSDir removes the directory of a deleted session when every
// entry in it is a stale file the sweep recognises; otherwise it leaves the
// directory untouched. Only a database error is returned.
func (s *Store) sweepOrphanFSDir(ctx context.Context, res *FSSweepResult, dir, slug string, cutoff time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		res.skip(slug)
		return nil
	}
	// Stat the directory after listing it: an entry added since then makes
	// its mtime fresh.
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		res.skip(slug)
		return nil
	}
	if !info.ModTime().Before(cutoff) {
		return nil
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || (!isFSFileID(e.Name()) && !isFSTempName(e.Name())) {
			res.skip(slug)
			return nil
		}
		fi, err := e.Info()
		if err != nil {
			res.skip(slug)
			return nil
		}
		if !fi.ModTime().Before(cutoff) {
			return nil
		}
	}
	// A session may have been created under this name since the snapshot.
	var one int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE slug = ?`, slug).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, e := range entries {
		err := os.Remove(filepath.Join(dir, e.Name()))
		if err == nil {
			res.RemovedFiles++
		} else if !errors.Is(err, os.ErrNotExist) {
			res.skip(slug)
			return nil
		}
	}
	// Fails, leaving the directory, if a file was written into it meanwhile.
	if err := os.Remove(dir); err == nil {
		res.RemovedDirs++
	} else if !errors.Is(err, os.ErrNotExist) {
		res.skip(slug)
	}
	return nil
}

// isFSFileID reports whether name looks like a file ID from session.NewSlug.
func isFSFileID(name string) bool {
	if len(name) != FileIDLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		if strings.IndexByte(session.Alphabet, name[i]) < 0 {
			return false
		}
	}
	return true
}

// isFSTempName reports whether name looks like a temporary file of
// writeFSFile: os.CreateTemp(dir, id+"-*.tmp") replaces "*" with digits.
func isFSTempName(name string) bool {
	base, ok := strings.CutSuffix(name, ".tmp")
	if !ok {
		return false
	}
	id, digits, ok := strings.Cut(base, "-")
	if !ok || digits == "" || !isFSFileID(id) {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}
