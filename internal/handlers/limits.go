package handlers

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"sharetext/internal/store"
)

// DefaultUploadConcurrency caps simultaneous uploads (each buffers up to
// MaxFileSize in memory).
const DefaultUploadConcurrency = 4

const storageCacheTTL = 5 * time.Second

type limitState struct {
	once    sync.Once
	sem     chan struct{}
	mu      sync.Mutex
	checked time.Time
	total   int64
	added   atomic.Int64
}

func (a *API) limits() *limitState {
	a.lim.once.Do(func() {
		n := a.UploadConcurrency
		if n <= 0 {
			n = DefaultUploadConcurrency
		}
		a.lim.sem = make(chan struct{}, n)
	})
	return &a.lim
}

func (a *API) acquireUpload() (func(), bool) {
	l := a.limits()
	select {
	case l.sem <- struct{}{}:
		return func() { <-l.sem }, true
	default:
		return func() {}, false
	}
}

// storageFull reports whether adding extra bytes would exceed
// MaxTotalStorageBytes. The total is cached briefly; bytes stored since the
// last refresh are tracked via noteStored. Errors fail open.
func (a *API) storageFull(ctx context.Context, extra int64) bool {
	if a.MaxTotalStorageBytes <= 0 || a.Store == nil {
		return false
	}
	l := a.limits()
	l.mu.Lock()
	if time.Since(l.checked) > storageCacheTTL {
		if t, err := a.Store.TotalBytes(ctx); err == nil {
			l.total = t
			l.checked = time.Now()
			l.added.Store(0)
		}
	}
	cur := l.total + l.added.Load()
	l.mu.Unlock()
	return cur+extra > a.MaxTotalStorageBytes
}

// updateContent saves content under the server-wide storage limit. Approved
// growth is counted only after the update committed: the store runs the hook
// again when it retries a busy transaction, and may still fail after it.
func (a *API) updateContent(ctx context.Context, slug, content string) (*store.Session, error) {
	if a.MaxTotalStorageBytes <= 0 {
		return a.Store.Update(ctx, slug, content)
	}
	var grew int64
	sess, err := a.Store.UpdateWithin(ctx, slug, content, func(delta int64) bool {
		grew = 0
		if a.storageFull(ctx, delta) {
			return false
		}
		grew = delta
		return true
	})
	if err == nil {
		a.noteStored(grew)
	}
	return sess, err
}

func (a *API) noteStored(n int64) {
	if a.MaxTotalStorageBytes > 0 {
		a.limits().added.Add(n)
	}
}
