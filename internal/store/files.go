package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
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
	ID          string
	SessionSlug string
	Filename    string
	MIME        string
	Size        int64
	Data        []byte
	CreatedAt   time.Time
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
	ok, err := s.Exists(ctx, slug)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	size := int64(len(data))
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO files (id, session_slug, filename, mime, size, data, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, slug, filename, mime, size, data, now.Unix())
	if err != nil {
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
}

// GetFile returns the binary content + metadata. Returns ErrNotFound if missing
// or ErrExpired if the parent session is expired.
func (s *Store) GetFile(ctx context.Context, slug, id string) (*File, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, session_slug, filename, mime, size, data, created_at FROM files WHERE id = ? AND session_slug = ?`,
		id, slug)
	var f File
	var created int64
	if err := row.Scan(&f.ID, &f.SessionSlug, &f.Filename, &f.MIME, &f.Size, &f.Data, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	f.CreatedAt = time.Unix(created, 0).UTC()
	// Honour session-level expiry.
	sess, err := s.Get(ctx, slug)
	if err != nil {
		return nil, err
	}
	_ = sess
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

// DeleteOrphanFiles removes attachments that are no longer reachable:
//   - files whose session is gone (defensive net in case FK cascade is off);
//   - files older than `grace` whose ID does not appear in any session content
//     anymore (i.e. the marker was removed from the editor).
//
// The grace window prevents racing with a fresh upload whose marker has not
// yet been pushed via WS/PUT.
func (s *Store) DeleteOrphanFiles(ctx context.Context, grace time.Duration) (int64, error) {
	var total int64

	// A) safety net: files referencing missing sessions.
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

	// B) per-session unreferenced markers.
	cutoff := time.Now().Add(-grace).Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT slug, content FROM sessions`)
	if err != nil {
		return total, err
	}
	type entry struct {
		slug    string
		content string
	}
	var sessions []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.slug, &e.content); err != nil {
			rows.Close()
			return total, err
		}
		sessions = append(sessions, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return total, err
	}
	rows.Close()

	for _, e := range sessions {
		refs := ReferencedFileIDs(e.content)
		var (
			res sql.Result
			err error
		)
		if len(refs) == 0 {
			res, err = s.db.ExecContext(ctx,
				`DELETE FROM files WHERE session_slug = ? AND created_at <= ?`,
				e.slug, cutoff)
		} else {
			placeholders := make([]string, 0, len(refs))
			args := []any{e.slug, cutoff}
			for id := range refs {
				placeholders = append(placeholders, "?")
				args = append(args, id)
			}
			q := `DELETE FROM files WHERE session_slug = ? AND created_at <= ? AND id NOT IN (` +
				strings.Join(placeholders, ",") + `)`
			res, err = s.db.ExecContext(ctx, q, args...)
		}
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// DeleteFile removes a single attachment.
func (s *Store) DeleteFile(ctx context.Context, slug, id string) error {
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
	return nil
}
