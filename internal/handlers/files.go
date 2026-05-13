package handlers

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"sharetext/internal/session"
	"sharetext/internal/store"
)

// MaxFileSize is the per-upload limit in bytes (set from main via env).
// Default is wired from CLI; safe fallback here.
var MaxFileSize int64 = 10 * 1024 * 1024

type fileResp struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url"`
	Marker    string    `json:"marker"`
}

func fileMarker(id, filename string) string {
	return fmt.Sprintf("[file:%s:%s]", id, url.PathEscape(filename))
}

func toFileResp(slug string, f *store.FileSummary) fileResp {
	return fileResp{
		ID:        f.ID,
		Filename:  f.Filename,
		MIME:      f.MIME,
		Size:      f.Size,
		CreatedAt: f.CreatedAt,
		URL:       "/api/sessions/" + slug + "/files/" + f.ID,
		Marker:    fileMarker(f.ID, f.Filename),
	}
}

// UploadFile accepts multipart/form-data with field "file" and stores it.
func (a *API) UploadFile(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	ok, err := a.Store.Exists(r.Context(), slug)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	clientID := strings.TrimSpace(r.Header.Get(ClientIDHeader))
	snap, lockChanged, allowed := a.tryWriteLock(slug, clientID)
	if !allowed {
		writeLockDenied(w, snap)
		return
	}
	if lockChanged && a.Hub != nil && a.Locks != nil {
		a.broadcastLock(slug, a.Locks.State(slug), nil)
	}
	// Reject oversize early via MaxBytesReader.
	r.Body = http.MaxBytesReader(w, r.Body, MaxFileSize+512*1024) // +slack for multipart overhead
	if err := r.ParseMultipartForm(MaxFileSize + 512*1024); err != nil {
		http.Error(w, "upload too large or malformed", http.StatusRequestEntityTooLarge)
		return
	}
	fh, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer fh.Close()
	if header.Size > MaxFileSize {
		http.Error(w, "file exceeds max size", http.StatusRequestEntityTooLarge)
		return
	}

	buf := make([]byte, 0, header.Size)
	tmp := make([]byte, 32*1024)
	for {
		n, rerr := fh.Read(tmp)
		if n > 0 {
			if int64(len(buf)+n) > MaxFileSize {
				http.Error(w, "file exceeds max size", http.StatusRequestEntityTooLarge)
				return
			}
			buf = append(buf, tmp[:n]...)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}
	}

	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}
	filename := sanitizeUploadName(header.Filename)

	id, err := session.NewSlug(12)
	if err != nil {
		http.Error(w, "id gen failed", http.StatusInternalServerError)
		return
	}

	sum, err := a.Store.AddFile(r.Context(), slug, id, filename, mime, buf)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if a.Metrics != nil {
		a.Metrics.IncFilesUploaded()
	}
	writeJSON(w, http.StatusCreated, toFileResp(slug, sum))
}

// DownloadFile streams the file with Content-Disposition: attachment.
func (a *API) DownloadFile(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	id := chi.URLParam(r, "id")
	f, err := a.Store.GetFile(r.Context(), slug, id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if a.Metrics != nil {
		a.Metrics.IncFileDownloads()
	}
	w.Header().Set("Content-Type", f.MIME)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", f.Size))
	w.Header().Set("Content-Disposition", contentDisposition(f.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(f.Data)
}

// ListFiles returns metadata-only list of attachments for a session.
func (a *API) ListFiles(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if _, err := a.Store.GetSessionState(r.Context(), slug); err != nil {
		writeStoreErr(w, err)
		return
	}
	list, err := a.Store.ListFiles(r.Context(), slug)
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]fileResp, 0, len(list))
	for i := range list {
		out = append(out, toFileResp(slug, &list[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out, "count": len(out)})
}

// Bundle streams a zip containing the session text + all attachments.
func (a *API) Bundle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	sess, err := a.Store.Get(r.Context(), slug)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	files, err := a.Store.ListBundleFiles(r.Context(), slug)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if a.Metrics != nil {
		a.Metrics.IncBundlesGenerated()
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(slug+".zip"))
	w.Header().Set("X-Content-Type-Options", "nosniff")

	zw := zip.NewWriter(w)
	defer zw.Close()

	// session text
	txtName := slug + ".txt"
	tw, err := zw.Create(txtName)
	if err != nil {
		return
	}
	if _, err := io.WriteString(tw, sess.Content); err != nil {
		return
	}

	// attachments
	seen := make(map[string]int)
	for _, f := range files {
		name := "files/" + uniqueZipName(seen, f.Filename)
		fw, err := zw.Create(name)
		if err != nil {
			return
		}
		if _, err := fw.Write(f.Data); err != nil {
			return
		}
	}
}

func uniqueZipName(seen map[string]int, name string) string {
	base := sanitizeUploadName(name)
	if _, dup := seen[base]; !dup {
		seen[base] = 1
		return base
	}
	for {
		seen[base]++
		idx := seen[base]
		candidate := withSuffix(base, idx)
		if _, exists := seen[candidate]; !exists {
			seen[candidate] = 1
			return candidate
		}
	}
}

func withSuffix(name string, idx int) string {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		return fmt.Sprintf("%s-%d", name, idx)
	}
	return fmt.Sprintf("%s-%d%s", name[:dot], idx, name[dot:])
}

func sanitizeUploadName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "file.bin"
	}
	// Drop any directory components from the client.
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	if s == "" || s == "." || s == ".." {
		return "file.bin"
	}
	// Remove control chars.
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
	if len(cleaned) > 200 {
		cleaned = cleaned[:200]
	}
	if cleaned == "" {
		return "file.bin"
	}
	return cleaned
}

func contentDisposition(filename string) string {
	// RFC 5987 encoded value for non-ASCII safety, plus ASCII fallback.
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, filename)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(filename))
}

// errorsIsRequestEntityTooLarge — sentinel helper kept for future use.
var _ = errors.New
