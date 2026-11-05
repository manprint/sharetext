package handlers

import (
	"archive/zip"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"sharetext/internal/session"
	"sharetext/internal/store"
)

// DefaultMaxFileSize is the per-upload limit in bytes used when
// API.MaxFileSize is unset.
const DefaultMaxFileSize int64 = 10 * 1024 * 1024

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
	release, acquired := a.acquireUpload()
	if !acquired {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "too many uploads in progress", http.StatusServiceUnavailable)
		return
	}
	defer release()
	clientID := strings.TrimSpace(r.Header.Get(ClientIDHeader))
	if snap, allowed := a.tryWriteLock(slug, clientID, true); !allowed {
		writeLockDenied(w, snap)
		return
	}
	maxFile := a.maxFileSize()
	// Stream the multipart body instead of ParseMultipartForm, which would
	// keep a second in-memory copy of the whole file.
	r.Body = http.MaxBytesReader(w, r.Body, maxFile+512*1024) // +slack for multipart overhead
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "upload too large or malformed", http.StatusRequestEntityTooLarge)
		return
	}
	var part *multipart.Part
	for {
		p, perr := mr.NextPart()
		if perr == io.EOF {
			http.Error(w, "missing 'file' field", http.StatusBadRequest)
			return
		}
		if perr != nil {
			http.Error(w, "upload too large or malformed", http.StatusRequestEntityTooLarge)
			return
		}
		if p.FormName() == "file" && p.FileName() != "" {
			part = p
			break
		}
		_ = p.Close()
	}
	defer part.Close()
	buf, rerr := io.ReadAll(io.LimitReader(part, maxFile+1))
	if rerr != nil {
		http.Error(w, "upload too large or malformed", http.StatusRequestEntityTooLarge)
		return
	}
	if int64(len(buf)) > maxFile {
		http.Error(w, "file exceeds max size", http.StatusRequestEntityTooLarge)
		return
	}
	if a.storageFull(r.Context(), int64(len(buf))) {
		http.Error(w, "server storage full", http.StatusInsufficientStorage)
		return
	}

	mime := part.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}
	filename := sanitizeUploadName(part.FileName())

	id, err := session.NewSlug(store.FileIDLen)
	if err != nil {
		http.Error(w, "id gen failed", http.StatusInternalServerError)
		return
	}

	sum, err := a.Store.AddFile(r.Context(), slug, id, filename, mime, buf)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	a.noteStored(int64(len(buf)))
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
	w.Header().Set("Content-Type", downloadContentType(f.MIME))
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
	// Remove control chars (and repair invalid UTF-8, which strings.Map
	// turns into U+FFFD).
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	cleaned = truncateUTF8(cleaned, maxUploadNameBytes)
	if cleaned == "" {
		return "file.bin"
	}
	return cleaned
}

const maxUploadNameBytes = 200

// truncateUTF8 caps s at max bytes without splitting a multi-byte rune, which
// would leave an invalid UTF-8 tail in the stored name.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
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

// activeContentTypes are types a browser would execute or render as a
// document when opened from our origin. Served as opaque bytes instead.
var activeContentTypes = map[string]bool{
	"text/html":                true,
	"application/xhtml+xml":    true,
	"image/svg+xml":            true,
	"text/xml":                 true,
	"application/xml":          true,
	"text/javascript":          true,
	"application/javascript":   true,
	"application/x-javascript": true,
	"text/css":                 true,
	"application/pdf":          true,
}

// downloadContentType returns the Content-Type to serve for an upload: the
// client-declared one, except active types (and anything unparsable), which
// become application/octet-stream.
func downloadContentType(declared string) string {
	mt, _, err := mime.ParseMediaType(declared)
	if err != nil || activeContentTypes[strings.ToLower(mt)] {
		return "application/octet-stream"
	}
	return declared
}
