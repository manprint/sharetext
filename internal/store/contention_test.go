package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentWritersUnderContention hammers one session with interleaved
// text updates and uploads from many goroutines. Every write must land: a
// writer that loses a race has to wait for the lock, not fail with
// SQLITE_BUSY once the retry budget runs out.
func TestConcurrentWritersUnderContention(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "busy"}); err != nil {
		t.Fatal(err)
	}
	const writers, rounds = 32, 15
	var wg sync.WaitGroup
	errs := make(chan error, writers*rounds)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				var err error
				if (w+i)%4 == 0 {
					_, err = s.AddFile(ctx, "busy", fmt.Sprintf("f%d-%d", w, i), "a.txt", "text/plain", []byte("payload"))
				} else {
					_, err = s.Update(ctx, "busy", fmt.Sprintf("writer %d round %d [file:f%d-%d:a.txt]", w, i, w, i))
				}
				if err != nil {
					errs <- fmt.Errorf("writer %d round %d: %w", w, i, err)
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestStorageQuotaCountsContentBytesNotCharacters(t *testing.T) {
	s := openTestWithOptions(t, Options{MaxSessionStorageBytes: 10})
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "utf8"}); err != nil {
		t.Fatal(err)
	}
	// 4 characters, 8 bytes.
	if _, err := s.Update(ctx, "utf8", "èèèè"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFile(ctx, "utf8", "f1", "a.bin", "application/octet-stream", []byte("abc")); !errors.Is(err, ErrSessionStorageExceeded) {
		t.Fatalf("8 content bytes + 3 file bytes exceed a 10-byte quota, got err=%v", err)
	}
	items, err := s.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ContentSize != 8 {
		t.Fatalf("admin size must be reported in bytes, got %+v", items)
	}
}

func TestDeleteRemovesFSDirectoryEvenWithoutRows(t *testing.T) {
	s := openTestWithOptions(t, Options{FileBackend: FileBackendFS})
	ctx := context.Background()
	if _, err := s.Create(ctx, CreateOpts{Slug: "fsdel"}); err != nil {
		t.Fatal(err)
	}
	// A file that reached the disk without (yet) a row, as when an upload
	// races the delete.
	if err := s.writeFSFile("fsdel", "stray", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "fsdel"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.opts.FileStorageDir, "fsdel")); !os.IsNotExist(err) {
		t.Fatalf("session directory must be removed, stat err=%v", err)
	}
}
