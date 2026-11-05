package store

import (
	"context"
	"time"
)

// Ping reports whether the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// TotalBytes returns the bytes held for all sessions: text plus attachments.
func (s *Store) TotalBytes(ctx context.Context) (int64, error) {
	var text, files int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(length(CAST(content AS BLOB))), 0) FROM sessions`).Scan(&text); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(size), 0) FROM files`).Scan(&files); err != nil {
		return 0, err
	}
	return text + files, nil
}

// DeleteIdlePersistent removes persistent sessions (no expiry) not updated
// since cutoff. Attachment directories left by the fs backend are collected
// by SweepFSOrphans. Returns the number of sessions deleted.
func (s *Store) DeleteIdlePersistent(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at IS NULL AND updated_at < ?`, cutoff.UTC().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
