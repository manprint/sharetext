package telemetry

import (
	"sync/atomic"
	"time"
)

type Snapshot struct {
	Enabled                bool       `json:"enabled"`
	SessionsCreated        int64      `json:"sessions_created"`
	SessionUpdates         int64      `json:"session_updates"`
	FilesUploaded          int64      `json:"files_uploaded"`
	FileDownloads          int64      `json:"file_downloads"`
	BundlesGenerated       int64      `json:"bundles_generated"`
	CleanupRuns            int64      `json:"cleanup_runs"`
	CleanupDeletedSessions int64      `json:"cleanup_deleted_sessions"`
	CleanupDeletedFiles    int64      `json:"cleanup_deleted_files"`
	VacuumRuns             int64      `json:"vacuum_runs"`
	LastCleanupAt          *time.Time `json:"last_cleanup_at,omitempty"`
	LastCleanupDurationMS  int64      `json:"last_cleanup_duration_ms"`
	LastVacuumAt           *time.Time `json:"last_vacuum_at,omitempty"`
	LastVacuumDurationMS   int64      `json:"last_vacuum_duration_ms"`
}

type Metrics struct {
	enabled                bool
	sessionsCreated        atomic.Int64
	sessionUpdates         atomic.Int64
	filesUploaded          atomic.Int64
	fileDownloads          atomic.Int64
	bundlesGenerated       atomic.Int64
	cleanupRuns            atomic.Int64
	cleanupDeletedSessions atomic.Int64
	cleanupDeletedFiles    atomic.Int64
	vacuumRuns             atomic.Int64
	lastCleanupAtUnix      atomic.Int64
	lastCleanupDurationMS  atomic.Int64
	lastVacuumAtUnix       atomic.Int64
	lastVacuumDurationMS   atomic.Int64
}

func NewMetrics(enabled bool) *Metrics {
	return &Metrics{enabled: enabled}
}

func (m *Metrics) Enabled() bool {
	return m != nil && m.enabled
}

func (m *Metrics) IncSessionsCreated() {
	if m.Enabled() {
		m.sessionsCreated.Add(1)
	}
}

func (m *Metrics) IncSessionUpdates() {
	if m.Enabled() {
		m.sessionUpdates.Add(1)
	}
}

func (m *Metrics) IncFilesUploaded() {
	if m.Enabled() {
		m.filesUploaded.Add(1)
	}
}

func (m *Metrics) IncFileDownloads() {
	if m.Enabled() {
		m.fileDownloads.Add(1)
	}
}

func (m *Metrics) IncBundlesGenerated() {
	if m.Enabled() {
		m.bundlesGenerated.Add(1)
	}
}

func (m *Metrics) ObserveCleanup(deletedSessions, deletedFiles int64, duration time.Duration) {
	if !m.Enabled() {
		return
	}
	m.cleanupRuns.Add(1)
	m.cleanupDeletedSessions.Add(deletedSessions)
	m.cleanupDeletedFiles.Add(deletedFiles)
	m.lastCleanupAtUnix.Store(time.Now().UTC().Unix())
	m.lastCleanupDurationMS.Store(duration.Milliseconds())
}

func (m *Metrics) ObserveVacuum(duration time.Duration) {
	if !m.Enabled() {
		return
	}
	m.vacuumRuns.Add(1)
	m.lastVacuumAtUnix.Store(time.Now().UTC().Unix())
	m.lastVacuumDurationMS.Store(duration.Milliseconds())
}

func (m *Metrics) Snapshot() Snapshot {
	if !m.Enabled() {
		return Snapshot{Enabled: false}
	}
	cleanupAt := unixPtr(m.lastCleanupAtUnix.Load())
	vacuumAt := unixPtr(m.lastVacuumAtUnix.Load())
	return Snapshot{
		Enabled:                true,
		SessionsCreated:        m.sessionsCreated.Load(),
		SessionUpdates:         m.sessionUpdates.Load(),
		FilesUploaded:          m.filesUploaded.Load(),
		FileDownloads:          m.fileDownloads.Load(),
		BundlesGenerated:       m.bundlesGenerated.Load(),
		CleanupRuns:            m.cleanupRuns.Load(),
		CleanupDeletedSessions: m.cleanupDeletedSessions.Load(),
		CleanupDeletedFiles:    m.cleanupDeletedFiles.Load(),
		VacuumRuns:             m.vacuumRuns.Load(),
		LastCleanupAt:          cleanupAt,
		LastCleanupDurationMS:  m.lastCleanupDurationMS.Load(),
		LastVacuumAt:           vacuumAt,
		LastVacuumDurationMS:   m.lastVacuumDurationMS.Load(),
	}
}

func unixPtr(unix int64) *time.Time {
	if unix <= 0 {
		return nil
	}
	t := time.Unix(unix, 0).UTC()
	return &t
}
