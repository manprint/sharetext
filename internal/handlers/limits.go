package handlers

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
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

func (a *API) noteStored(n int64) {
	if a.MaxTotalStorageBytes > 0 {
		a.limits().added.Add(n)
	}
}
