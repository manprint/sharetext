package store

import (
	"context"
	"time"
)

type AuditEntry struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Details   string    `json:"details,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) RecordAudit(ctx context.Context, entry AuditEntry) error {
	if !s.opts.AuditLogEnabled {
		return nil
	}
	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_logs (actor, action, target, details, created_at) VALUES (?, ?, ?, ?, ?)`,
		entry.Actor, entry.Action, entry.Target, entry.Details, createdAt.Unix())
	return err
}

func (s *Store) ListAuditLogs(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, actor, action, target, details, created_at FROM audit_logs ORDER BY created_at DESC, id DESC LIMIT ?`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		var created int64
		if err := rows.Scan(&entry.ID, &entry.Actor, &entry.Action, &entry.Target, &entry.Details, &created); err != nil {
			return nil, err
		}
		entry.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *Store) AuditEnabled() bool {
	return s.opts.AuditLogEnabled
}
