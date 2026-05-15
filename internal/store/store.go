package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound               = errors.New("session not found")
	ErrExpired                = errors.New("session expired")
	ErrTooManyFiles           = errors.New("too many files in session")
	ErrSessionStorageExceeded = errors.New("session storage limit exceeded")
)

type Session struct {
	Slug      string
	Name      string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt *time.Time
}

type SessionState struct {
	Slug      string
	ExpiresAt *time.Time
}

func (s *Session) IsExpired(now time.Time) bool {
	return s.ExpiresAt != nil && !now.Before(*s.ExpiresAt)
}

type CreateOpts struct {
	Slug      string
	Name      string
	ExpiresAt *time.Time
}

type Store struct {
	db   *sql.DB
	path string
	opts Options
}

func Open(path string) (*Store, error) {
	return OpenWithOptions(path, Options{})
}

func OpenWithOptions(path string, opts Options) (*Store, error) {
	normalized, err := opts.normalized(path)
	if err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	if normalized.SecureDelete {
		dsn += "&_pragma=secure_delete(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if normalized.FileBackend == FileBackendFS {
		if err := os.MkdirAll(normalized.FileStorageDir, 0o755); err != nil {
			return nil, err
		}
	}
	s := &Store{db: db, path: path, opts: normalized}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
    slug TEXT PRIMARY KEY,
    name TEXT,
    content TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    expires_at INTEGER
);`); err != nil {
		return err
	}
	// Best-effort schema upgrade for DBs created before name/expires_at existed.
	addColumn := func(sql string) error {
		_, err := s.db.Exec(sql)
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
		return nil
	}
	if err := addColumn(`ALTER TABLE sessions ADD COLUMN name TEXT`); err != nil {
		return err
	}
	if err := addColumn(`ALTER TABLE sessions ADD COLUMN expires_at INTEGER`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at) WHERE expires_at IS NOT NULL`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS files (
    id TEXT PRIMARY KEY,
    session_slug TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime TEXT NOT NULL,
    size INTEGER NOT NULL,
    data BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (session_slug) REFERENCES sessions(slug) ON DELETE CASCADE
);`); err != nil {
		return err
	}
	if err := addColumn(`ALTER TABLE files ADD COLUMN storage_backend TEXT NOT NULL DEFAULT 'db'`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_files_session ON files(session_slug)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_files_session_created ON files(session_slug, created_at)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS file_refs (
    session_slug TEXT NOT NULL,
    file_id TEXT NOT NULL,
    PRIMARY KEY (session_slug, file_id),
    FOREIGN KEY (session_slug) REFERENCES sessions(slug) ON DELETE CASCADE
);`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_file_refs_session ON file_refs(session_slug)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    details TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at DESC)`); err != nil {
		return err
	}
	return s.backfillFileRefsIfNeeded()
}

func (s *Store) Create(ctx context.Context, opts CreateOpts) (*Session, error) {
	now := time.Now().UTC()
	var expUnix sql.NullInt64
	if opts.ExpiresAt != nil {
		expUnix = sql.NullInt64{Int64: opts.ExpiresAt.Unix(), Valid: true}
	}
	var nameVal sql.NullString
	if opts.Name != "" {
		nameVal = sql.NullString{String: opts.Name, Valid: true}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (slug, name, content, created_at, updated_at, expires_at) VALUES (?, ?, '', ?, ?, ?)`,
		opts.Slug, nameVal, now.Unix(), now.Unix(), expUnix)
	if err != nil {
		return nil, err
	}
	sess := &Session{Slug: opts.Slug, Name: opts.Name, CreatedAt: now, UpdatedAt: now}
	if opts.ExpiresAt != nil {
		t := opts.ExpiresAt.UTC()
		sess.ExpiresAt = &t
	}
	return sess, nil
}

func scanSession(row interface {
	Scan(...any) error
}) (*Session, error) {
	var sess Session
	var name sql.NullString
	var created, updated int64
	var exp sql.NullInt64
	if err := row.Scan(&sess.Slug, &name, &sess.Content, &created, &updated, &exp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if name.Valid {
		sess.Name = name.String
	}
	sess.CreatedAt = time.Unix(created, 0).UTC()
	sess.UpdatedAt = time.Unix(updated, 0).UTC()
	if exp.Valid {
		t := time.Unix(exp.Int64, 0).UTC()
		sess.ExpiresAt = &t
	}
	return &sess, nil
}

func (s *Store) Get(ctx context.Context, slug string) (*Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT slug, name, content, created_at, updated_at, expires_at FROM sessions WHERE slug = ?`, slug)
	sess, err := scanSession(row)
	if err != nil {
		return nil, err
	}
	if sess.IsExpired(time.Now().UTC()) {
		return nil, ErrExpired
	}
	return sess, nil
}

func (s *Store) Update(ctx context.Context, slug, content string) (*Session, error) {
	return retryOnBusy(func() (*Session, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()

		stats, err := s.sessionStatsTx(ctx, tx, slug)
		if err != nil {
			return nil, err
		}
		if s.opts.MaxSessionStorageBytes > 0 && int64(len(content))+stats.FilesSize > s.opts.MaxSessionStorageBytes {
			return nil, ErrSessionStorageExceeded
		}
		now := time.Now().UTC()
		res, err := tx.ExecContext(ctx,
			`UPDATE sessions SET content = ?, updated_at = ? WHERE slug = ?`,
			content, now.Unix(), slug)
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, ErrNotFound
		}
		if err := s.syncSessionRefsTx(ctx, tx, slug, content); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &Session{
			Slug:      slug,
			Name:      stats.Name,
			Content:   content,
			CreatedAt: stats.CreatedAt,
			UpdatedAt: now,
			ExpiresAt: stats.ExpiresAt,
		}, nil
	})
}

func (s *Store) GetSessionState(ctx context.Context, slug string) (*SessionState, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT slug, expires_at FROM sessions WHERE slug = ?`, slug)
	var state SessionState
	var exp sql.NullInt64
	if err := row.Scan(&state.Slug, &exp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if exp.Valid {
		t := time.Unix(exp.Int64, 0).UTC()
		state.ExpiresAt = &t
	}
	if state.ExpiresAt != nil && !time.Now().UTC().Before(*state.ExpiresAt) {
		return nil, ErrExpired
	}
	return &state, nil
}

func (s *Store) Exists(ctx context.Context, slug string) (bool, error) {
	_, err := s.GetSessionState(ctx, slug)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrExpired) {
		return false, nil
	}
	return false, err
}

type sessionStats struct {
	Slug        string
	Name        string
	ContentSize int64
	FilesSize   int64
	FilesCount  int
	CreatedAt   time.Time
	ExpiresAt   *time.Time
}

func getSessionStats(row interface {
	Scan(...any) error
}) (*sessionStats, error) {
	var stats sessionStats
	var name sql.NullString
	var created int64
	var exp sql.NullInt64
	if err := row.Scan(&stats.Slug, &name, &created, &exp, &stats.ContentSize, &stats.FilesSize, &stats.FilesCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if name.Valid {
		stats.Name = name.String
	}
	stats.CreatedAt = time.Unix(created, 0).UTC()
	if exp.Valid {
		t := time.Unix(exp.Int64, 0).UTC()
		stats.ExpiresAt = &t
	}
	if stats.ExpiresAt != nil && !time.Now().UTC().Before(*stats.ExpiresAt) {
		return nil, ErrExpired
	}
	return &stats, nil
}

func (s *Store) sessionStatsTx(ctx context.Context, tx *sql.Tx, slug string) (*sessionStats, error) {
	row := tx.QueryRowContext(ctx, `SELECT slug, name, created_at, expires_at,
		COALESCE(length(content), 0),
		COALESCE((SELECT SUM(size) FROM files WHERE session_slug = ?), 0),
		COALESCE((SELECT COUNT(*) FROM files WHERE session_slug = ?), 0)
		FROM sessions WHERE slug = ?`, slug, slug, slug)
	return getSessionStats(row)
}

func retryOnBusy[T any](fn func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; attempt < 10; attempt++ {
		value, err := fn()
		if !isSQLiteBusy(err) || attempt == 9 {
			return value, err
		}
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
	return zero, nil
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "DATABASE IS LOCKED")
}

type SessionSummary struct {
	Slug        string
	Name        string
	ContentSize int
	FilesSize   int64
	FilesCount  int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   *time.Time
}

// TotalSize returns text + attachments bytes.
func (s SessionSummary) TotalSize() int64 {
	return int64(s.ContentSize) + s.FilesSize
}

// ListActive returns all non-expired sessions ordered by created_at desc.
func (s *Store) ListActive(ctx context.Context) ([]SessionSummary, error) {
	now := time.Now().UTC().Unix()
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.slug, s.name, length(s.content), s.created_at, s.updated_at, s.expires_at,
		        COALESCE((SELECT SUM(size) FROM files f WHERE f.session_slug = s.slug), 0) AS files_size,
		        COALESCE((SELECT COUNT(*)  FROM files f WHERE f.session_slug = s.slug), 0) AS files_count
		   FROM sessions s
		  WHERE s.expires_at IS NULL OR s.expires_at > ?
		  ORDER BY s.created_at DESC`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionSummary
	for rows.Next() {
		var (
			it      SessionSummary
			name    sql.NullString
			created int64
			updated int64
			exp     sql.NullInt64
		)
		if err := rows.Scan(&it.Slug, &name, &it.ContentSize, &created, &updated, &exp, &it.FilesSize, &it.FilesCount); err != nil {
			return nil, err
		}
		if name.Valid {
			it.Name = name.String
		}
		it.CreatedAt = time.Unix(created, 0).UTC()
		it.UpdatedAt = time.Unix(updated, 0).UTC()
		if exp.Valid {
			t := time.Unix(exp.Int64, 0).UTC()
			it.ExpiresAt = &t
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// DeleteExpired removes all sessions with expires_at <= now. Returns number deleted.
func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	fsSlugs, err := s.expiredFSSessionSlugs(ctx)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at IS NOT NULL AND expires_at <= ?`,
		time.Now().UTC().Unix())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	for _, slug := range fsSlugs {
		_ = s.removeSessionDir(slug)
	}
	return n, nil
}

// VacuumStats reports filesystem sizes (in bytes) of the SQLite main file and
// its WAL sidecar before/after a Vacuum run. Sizes are -1 when stat fails
// (e.g. WAL absent on a brand-new DB), but those don't fail the operation.
type VacuumStats struct {
	DBPath        string
	DBSizeBefore  int64
	DBSizeAfter   int64
	WALSizeBefore int64
	WALSizeAfter  int64
	Duration      time.Duration
}

// Vacuum runs VACUUM to reclaim free pages on disk, then truncates the WAL
// with a checkpoint(TRUNCATE). Order matters in WAL journal mode: VACUUM
// writes through the WAL, so the checkpoint comes *after* to leave the
// sidecar empty. A pre-checkpoint also runs so VACUUM starts from a clean
// state and the operation is idempotent under repeated runs.
//
// All statements run on a single dedicated connection because VACUUM cannot
// execute inside an explicit transaction. Returns stats for logging.
//
// NOTE: VACUUM rewrites the entire database; while it runs writers are
// blocked. Readers using WAL snapshots remain unaffected until the final
// commit. On a 40MB DB this is sub-second; size accordingly when scheduling.
func (s *Store) Vacuum(ctx context.Context) (VacuumStats, error) {
	stats := VacuumStats{DBPath: s.path}
	stats.DBSizeBefore, stats.WALSizeBefore = s.fileSizes()

	start := time.Now()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return stats, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Close()

	// Pre-checkpoint: flush any pending WAL into the main file. Failure here
	// (e.g. busy with other readers) is non-fatal; VACUUM still works.
	_, _ = conn.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		return stats, fmt.Errorf("vacuum: %w", err)
	}
	// Post-checkpoint: truncate the WAL pages produced by VACUUM itself.
	_, _ = conn.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)

	stats.Duration = time.Since(start)
	stats.DBSizeAfter, stats.WALSizeAfter = s.fileSizes()
	return stats, nil
}

func (s *Store) fileSizes() (db int64, wal int64) {
	db, wal = -1, -1
	if s.path == "" {
		return
	}
	if fi, err := os.Stat(s.path); err == nil {
		db = fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		wal = fi.Size()
	}
	return
}

// Delete removes a single session by slug.
func (s *Store) Delete(ctx context.Context, slug string) error {
	hadFSFiles, err := s.sessionHasFSFiles(ctx, slug)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE slug = ?`, slug)
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
	if hadFSFiles {
		_ = s.removeSessionDir(slug)
	}
	return nil
}
