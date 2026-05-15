package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var fileMarkerIDRe = regexp.MustCompile(`\[file:([A-Za-z0-9_-]+):`)

// ReferencedFileIDs extracts every file marker id from content.
// Lenient: inline markers also count, to avoid false positives during sweep.
func ReferencedFileIDs(content string) map[string]struct{} {
	matches := fileMarkerIDRe.FindAllStringSubmatch(content, -1)
	out := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		out[m[1]] = struct{}{}
	}
	return out
}

type File struct {
	ID             string
	SessionSlug    string
	Filename       string
	MIME           string
	Size           int64
	Data           []byte
	CreatedAt      time.Time
	StorageBackend string
}

type FileSummary struct {
	ID          string
	SessionSlug string
	Filename    string
	MIME        string
	Size        int64
	CreatedAt   time.Time
}

// AddFile stores a binary attachment for an existing session.
// Returns ErrNotFound if the session doesn't exist (or is expired).
func (s *Store) AddFile(ctx context.Context, slug, id, filename, mime string, data []byte) (*FileSummary, error) {
	return retryOnBusy(func() (*FileSummary, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()

		stats, err := s.sessionStatsTx(ctx, tx, slug)
		if err != nil {
			if errors.Is(err, ErrExpired) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		if s.opts.MaxFilesPerSession > 0 && stats.FilesCount >= s.opts.MaxFilesPerSession {
			return nil, ErrTooManyFiles
		}
		size := int64(len(data))
		if s.opts.MaxSessionStorageBytes > 0 && stats.ContentSize+stats.FilesSize+size > s.opts.MaxSessionStorageBytes {
			return nil, ErrSessionStorageExceeded
		}

		now := time.Now().UTC()
		backend := s.opts.FileBackend
		storedData := data
		if backend == FileBackendFS {
			storedData = []byte{}
			if err := s.writeFSFile(slug, id, data); err != nil {
				return nil, err
			}
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO files (id, session_slug, filename, mime, size, data, created_at, storage_backend) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, slug, filename, mime, size, storedData, now.Unix(), backend)
		if err != nil {
			if backend == FileBackendFS {
				_ = s.removeFSFile(slug, id)
			}
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			if backend == FileBackendFS {
				_ = s.removeFSFile(slug, id)
			}
			return nil, err
		}

		return &FileSummary{
			ID:          id,
			SessionSlug: slug,
			Filename:    filename,
			MIME:        mime,
			Size:        size,
			CreatedAt:   now,
		}, nil
	})
}

// GetFile returns the binary content + metadata. Returns ErrNotFound if missing
// or ErrExpired if the parent session is expired.
func (s *Store) GetFile(ctx context.Context, slug, id string) (*File, error) {
	row := s.db.QueryRowContext(ctx, `SELECT f.id, f.session_slug, f.filename, f.mime, f.size, f.data, f.created_at, f.storage_backend, s.expires_at
		FROM files f
		JOIN sessions s ON s.slug = f.session_slug
		WHERE f.id = ? AND f.session_slug = ?`, id, slug)
	var f File
	var created int64
	var exp sql.NullInt64
	if err := row.Scan(&f.ID, &f.SessionSlug, &f.Filename, &f.MIME, &f.Size, &f.Data, &created, &f.StorageBackend, &exp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	f.CreatedAt = time.Unix(created, 0).UTC()
	if exp.Valid {
		t := time.Unix(exp.Int64, 0).UTC()
		if !time.Now().UTC().Before(t) {
			return nil, ErrExpired
		}
	}
	if f.StorageBackend == FileBackendFS {
		data, err := os.ReadFile(s.fsFilePath(slug, id))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		f.Data = data
	}
	return &f, nil
}

// ListFiles returns metadata-only summaries for a session.
func (s *Store) ListFiles(ctx context.Context, slug string) ([]FileSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_slug, filename, mime, size, created_at FROM files WHERE session_slug = ? ORDER BY created_at ASC`,
		slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileSummary
	for rows.Next() {
		var f FileSummary
		var created int64
		if err := rows.Scan(&f.ID, &f.SessionSlug, &f.Filename, &f.MIME, &f.Size, &created); err != nil {
			return nil, err
		}
		f.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) ListBundleFiles(ctx context.Context, slug string) ([]File, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_slug, filename, mime, size, data, created_at, storage_backend FROM files WHERE session_slug = ? ORDER BY created_at ASC`,
		slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		var created int64
		if err := rows.Scan(&f.ID, &f.SessionSlug, &f.Filename, &f.MIME, &f.Size, &f.Data, &created, &f.StorageBackend); err != nil {
			return nil, err
		}
		f.CreatedAt = time.Unix(created, 0).UTC()
		if f.StorageBackend == FileBackendFS {
			data, err := os.ReadFile(s.fsFilePath(f.SessionSlug, f.ID))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil, ErrNotFound
				}
				return nil, err
			}
			f.Data = data
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteOrphanFiles removes attachments whose parent session row is already
// gone. This is a defensive safety net in case FK cascade is disabled or the
// DB/filesystem drift out of sync; attachments belonging to a live session are
// intentionally preserved for the full session lifetime.
func (s *Store) DeleteOrphanFiles(ctx context.Context) (int64, error) {
	var total int64

	missingSlugs, err := s.fsSessionSlugsWithoutSession(ctx)
	if err != nil {
		return total, err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM files WHERE session_slug NOT IN (SELECT slug FROM sessions)`)
	if err != nil {
		return total, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return total, err
	}
	total += n
	for _, slug := range missingSlugs {
		_ = s.removeSessionDir(slug)
	}
	return total, nil
}

// DeleteFile removes a single attachment.
func (s *Store) DeleteFile(ctx context.Context, slug, id string) error {
	backend, err := s.fileBackendForID(ctx, slug, id)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ? AND session_slug = ?`, id, slug)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if backend == FileBackendFS {
		_ = s.removeFSFile(slug, id)
	}
	return nil
}

func (s *Store) syncSessionRefsTx(ctx context.Context, tx *sql.Tx, slug, content string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM file_refs WHERE session_slug = ?`, slug); err != nil {
		return err
	}
	refs := ReferencedFileIDs(content)
	if len(refs) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO file_refs (session_slug, file_id) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC().Unix()
	mark, err := tx.PrepareContext(ctx, `UPDATE files SET last_referenced_at = ? WHERE id = ? AND session_slug = ?`)
	if err != nil {
		return err
	}
	defer mark.Close()
	for id := range refs {
		if _, err := stmt.ExecContext(ctx, slug, id); err != nil {
			return err
		}
		// Best-effort mark: a marker that points at a non-existent file id is
		// not an error (legacy content, manually edited marker). Just skip.
		if _, err := mark.ExecContext(ctx, now, id, slug); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) backfillFileRefsIfNeeded() error {
	var refsCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM file_refs`).Scan(&refsCount); err != nil {
		return err
	}
	if refsCount != 0 {
		return nil
	}
	var hasMarkers int
	if err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE content LIKE '%[file:%' LIMIT 1`).Scan(&hasMarkers); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(context.Background(), `SELECT slug, content FROM sessions`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var content string
		if err := rows.Scan(&slug, &content); err != nil {
			return err
		}
		if err := s.syncSessionRefsTx(context.Background(), tx, slug, content); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) fsFilePath(slug, id string) string {
	return filepath.Join(s.opts.FileStorageDir, slug, id)
}

func (s *Store) writeFSFile(slug, id string, data []byte) error {
	dir := filepath.Join(s.opts.FileStorageDir, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, id+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.fsFilePath(slug, id))
}

func (s *Store) removeFSFile(slug, id string) error {
	err := os.Remove(s.fsFilePath(slug, id))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(filepath.Join(s.opts.FileStorageDir, slug))
	return nil
}

func (s *Store) removeSessionDir(slug string) error {
	err := os.RemoveAll(filepath.Join(s.opts.FileStorageDir, slug))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) fileBackendForID(ctx context.Context, slug, id string) (string, error) {
	var backend string
	if err := s.db.QueryRowContext(ctx, `SELECT storage_backend FROM files WHERE id = ? AND session_slug = ?`, id, slug).Scan(&backend); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return backend, nil
}

func (s *Store) sessionHasFSFiles(ctx context.Context, slug string) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM files WHERE session_slug = ? AND storage_backend = ?)`, slug, FileBackendFS).Scan(&exists); err != nil {
		return false, err
	}
	return exists == 1, nil
}

func (s *Store) expiredFSSessionSlugs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT f.session_slug
		FROM files f
		JOIN sessions s ON s.slug = f.session_slug
		WHERE f.storage_backend = ? AND s.expires_at IS NOT NULL AND s.expires_at <= ?`, FileBackendFS, time.Now().UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

func (s *Store) fsSessionSlugsWithoutSession(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT session_slug FROM files WHERE storage_backend = ? AND session_slug NOT IN (SELECT slug FROM sessions)`, FileBackendFS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

