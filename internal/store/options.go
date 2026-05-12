package store

import (
	"fmt"
	"path/filepath"
)

const (
	FileBackendDB = "db"
	FileBackendFS = "fs"
)

type Options struct {
	FileBackend            string
	FileStorageDir         string
	MaxFilesPerSession     int
	MaxSessionStorageBytes int64
	AuditLogEnabled        bool
}

func (o Options) normalized(dbPath string) (Options, error) {
	switch o.FileBackend {
	case "", FileBackendDB:
		o.FileBackend = FileBackendDB
	case FileBackendFS:
		o.FileBackend = FileBackendFS
	default:
		return Options{}, fmt.Errorf("unsupported file backend %q", o.FileBackend)
	}
	if o.FileStorageDir == "" {
		o.FileStorageDir = filepath.Join(filepath.Dir(dbPath), "sharetext-files")
	}
	if o.MaxFilesPerSession < 0 {
		o.MaxFilesPerSession = 0
	}
	if o.MaxSessionStorageBytes < 0 {
		o.MaxSessionStorageBytes = 0
	}
	return o, nil
}
