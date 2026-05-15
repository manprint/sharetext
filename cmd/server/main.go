package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	texttemplate "text/template"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	"sharetext/internal/handlers"
	"sharetext/internal/store"
	"sharetext/internal/telemetry"
	"sharetext/internal/version"
)

//go:embed all:templates all:static
var assets embed.FS

//go:embed sw.js.tmpl
var swSource string

func init() {
	// Stdlib mime package lacks .webmanifest by default; without this, the
	// FileServer would serve the manifest as text/plain and browsers would
	// refuse to install the PWA.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

type pageData struct {
	Slug          string
	IdleReleaseMs int64
	ManifestPath  string
}

func main() {
	cfg := loadConfigFromEnv(os.Getenv)
	if cfg.AdminPassHash != "" {
		// Fail fast if ADMIN_PASS_HASH is not a valid bcrypt encoding (typo,
		// truncated copy/paste, wrong algorithm). Without this every login
		// silently returns 401 and the operator only finds out by trying.
		if _, err := bcrypt.Cost([]byte(cfg.AdminPassHash)); err != nil {
			log.Fatalf("ADMIN_PASS_HASH is not a valid bcrypt hash: %v", err)
		}
	}
	handlers.MaxFileSize = cfg.MaxFileSize
	handlers.MaxContentSize = cfg.MaxContentSize
	handlers.WSReadTimeout = cfg.WSReadTimeout
	metrics := telemetry.NewMetrics(cfg.MetricsEnabled)

	st, err := store.OpenWithOptions(cfg.DBPath, cfg.storeOptions())
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	api := &handlers.API{
		Store:                st,
		Hub:                  handlers.NewHub(),
		Locks:                handlers.NewLockManager(cfg.LockTTL),
		SlugLen:              cfg.SlugLen,
		Metrics:              metrics,
		AuditLogDefaultLimit: cfg.AuditLogDefaultLimit,
		AllowedOrigins:       cfg.AllowedOrigins,
	}

	tplFS, err := fs.Sub(assets, "templates")
	if err != nil {
		log.Fatalf("templates fs: %v", err)
	}
	tpl := template.New("").Funcs(template.FuncMap{
		"version": func() string { return version.Version },
	})
	tpl, err = tpl.ParseFS(tplFS, "*.html")
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}
	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}

	swTpl, err := texttemplate.New("sw").Parse(swSource)
	if err != nil {
		log.Fatalf("parse sw template: %v", err)
	}
	// BuildID changes whenever any embedded asset changes, even when Version
	// stays the same (typical in dev where the same tag is reused across
	// rebuilds). The service worker uses it in its cache name, so a new
	// deploy automatically evicts the stale code caches on the next page load
	// without any manual version bump. Data caches (api-, files-) are keyed
	// the same way — repopulated on next online fetch, no actual loss.
	buildID, err := computeBuildID(assets)
	if err != nil {
		log.Fatalf("compute build id: %v", err)
	}
	swData := struct {
		Version string
		BuildID string
	}{Version: version.Version, BuildID: buildID}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(handlers.SecurityHeaders(handlers.SecurityHeadersConfig{
		Enabled:                 cfg.SecurityHeadersEnabled,
		ContentSecurityPolicy:   cfg.ContentSecurityPolicy,
		FrameOptions:            cfg.FrameOptions,
		ReferrerPolicy:          cfg.ReferrerPolicy,
		PermissionsPolicy:       cfg.PermissionsPolicy,
		StrictTransportSecurity: cfg.StrictTransportSecurity,
	}))

	publicRateLimit := handlers.NewIPRateLimiter(handlers.RateLimitConfig{
		Enabled:           cfg.RateLimitEnabled,
		RequestsPerSecond: cfg.RateLimitRPS,
		Burst:             cfg.RateLimitBurst,
		EntryTTL:          cfg.RateLimitTTL,
	})
	adminRateLimit := handlers.NewIPRateLimiter(handlers.RateLimitConfig{
		Enabled:           cfg.RateLimitEnabled,
		RequestsPerSecond: cfg.AdminRateLimitRPS,
		Burst:             cfg.AdminRateLimitBurst,
		EntryTTL:          cfg.AdminRateLimitTTL,
	})
	// Session creation is the most expensive public endpoint (allocates a DB
	// row and a slug). Limit it tighter than the generic public bucket to keep
	// a single IP from filling storage with thousands of throwaway sessions.
	createRateLimit := handlers.NewIPRateLimiter(handlers.RateLimitConfig{
		Enabled:           cfg.RateLimitEnabled && cfg.CreateRateLimitEnabled,
		RequestsPerSecond: cfg.CreateRateLimitRPS,
		Burst:             cfg.CreateRateLimitBurst,
		EntryTTL:          cfg.CreateRateLimitTTL,
	})
	requestTimeout := middleware.Timeout(cfg.RequestTimeout)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Service worker must be served from root so its default scope covers '/'.
	// Cache-Control: no-cache so a new deploy is picked up on the next page
	// load; the SW body changes whenever version changes, which triggers the
	// browser's "byte-diff" install flow and the activate-side cache eviction.
	r.Get("/sw.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Service-Worker-Allowed", "/")
		if err := swTpl.Execute(w, swData); err != nil {
			log.Printf("render sw: %v", err)
		}
	})

	r.Group(func(g chi.Router) {
		g.Use(publicRateLimit)
		g.Get("/manifest/session/{slug}.webmanifest", func(w http.ResponseWriter, r *http.Request) {
			slug := chi.URLParam(r, "slug")
			ok, err := st.Exists(r.Context(), slug)
			if err != nil {
				http.Error(w, "internal", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			if err := json.NewEncoder(w).Encode(sessionManifest(slug)); err != nil {
				log.Printf("render session manifest: %v", err)
			}
		})
		g.With(requestTimeout).Get("/", func(w http.ResponseWriter, r *http.Request) {
			if err := tpl.ExecuteTemplate(w, "index.html", nil); err != nil {
				log.Printf("render index: %v", err)
			}
		})
		g.With(requestTimeout).Get("/launch/{slug}", func(w http.ResponseWriter, r *http.Request) {
			slug := chi.URLParam(r, "slug")
			if err := tpl.ExecuteTemplate(w, "launcher.html", pageData{Slug: slug, ManifestPath: sessionManifestPath(slug)}); err != nil {
				log.Printf("render launcher: %v", err)
			}
		})
		g.With(requestTimeout).Get("/s/{slug}", func(w http.ResponseWriter, r *http.Request) {
			slug := chi.URLParam(r, "slug")
			ok, err := st.Exists(r.Context(), slug)
			if err != nil {
				http.Error(w, "internal", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.NotFound(w, r)
				return
			}
			if err := tpl.ExecuteTemplate(w, "session.html", pageData{Slug: slug, IdleReleaseMs: cfg.LockIdleRelease.Milliseconds(), ManifestPath: sessionManifestPath(slug)}); err != nil {
				log.Printf("render session: %v", err)
			}
		})
		g.Group(func(apiRoutes chi.Router) {
			apiRoutes.Use(requestTimeout)
			apiRoutes.With(createRateLimit).Post("/api/sessions", api.CreateSession)
			apiRoutes.Get("/api/sessions/{slug}", api.GetSession)
			apiRoutes.Put("/api/sessions/{slug}", api.UpdateSession)
			apiRoutes.Post("/api/sessions/{slug}/files", api.UploadFile)
			apiRoutes.Get("/api/sessions/{slug}/files", api.ListFiles)
			apiRoutes.Get("/api/sessions/{slug}/files/{id}", api.DownloadFile)
			apiRoutes.Get("/api/sessions/{slug}/bundle", api.Bundle)
		})
		g.Get("/ws/{slug}", api.WS)
	})

	adminAuth := handlers.BasicAuth(handlers.BasicAuthConfig{
		User:     cfg.AdminUser,
		Pass:     cfg.AdminPass,
		PassHash: cfg.AdminPassHash,
		Realm:    "sharetext-admin",
	})
	r.Group(func(g chi.Router) {
		g.Use(adminRateLimit)
		g.Use(adminAuth)
		g.Use(requestTimeout)
		g.Get("/admin", func(w http.ResponseWriter, _ *http.Request) {
			if err := tpl.ExecuteTemplate(w, "admin.html", nil); err != nil {
				log.Printf("render admin: %v", err)
			}
		})
		g.Get("/admin/api/sessions", api.AdminList)
		g.Get("/admin/api/audit", api.AdminAudit)
		g.Get("/admin/api/metrics", api.AdminMetrics)
		g.Delete("/admin/api/sessions/{slug}", api.AdminDelete)
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runCleanup(ctx, st, cfg.CleanupInterval, metrics)
	if cfg.VacuumInterval > 0 {
		go runVacuum(ctx, st, cfg.VacuumInterval, metrics)
	}

	go func() {
		adminMode := "disabled"
		if cfg.AdminUser != "" && cfg.AdminPass != "" {
			adminMode = "enabled (user=" + cfg.AdminUser + ")"
		}
		vacuumMode := "disabled"
		if cfg.VacuumInterval > 0 {
			vacuumMode = cfg.VacuumInterval.String()
		}
		log.Printf("sharetext %s listening on :%s (db=%s, cleanup=%s, vacuum=%s, admin=%s, lock_ttl=%s, max_file=%dB, max_content=%dB, file_backend=%s)", version.Version, cfg.Port, cfg.DBPath, cfg.CleanupInterval, vacuumMode, adminMode, cfg.LockTTL, handlers.MaxFileSize, handlers.MaxContentSize, cfg.FileStorageBackend)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func runCleanup(ctx context.Context, st *store.Store, every time.Duration, metrics *telemetry.Metrics) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	doSweep := func() {
		started := time.Now()
		cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var deletedSessions int64
		var deletedFiles int64
		if n, err := st.DeleteExpired(cctx); err != nil {
			log.Printf("cleanup sessions: %v", err)
		} else if n > 0 {
			deletedSessions = n
			log.Printf("cleanup: deleted %d expired session(s)", n)
		}
		if n, err := st.DeleteOrphanFiles(cctx); err != nil {
			log.Printf("cleanup files: %v", err)
		} else if n > 0 {
			deletedFiles = n
			log.Printf("cleanup: deleted %d orphan file(s)", n)
		}
		if metrics != nil {
			metrics.ObserveCleanup(deletedSessions, deletedFiles, time.Since(started))
		}
	}
	doSweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			doSweep()
		}
	}
}

func runVacuum(ctx context.Context, st *store.Store, every time.Duration, metrics *telemetry.Metrics) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	doVacuum := func() {
		// Long generous timeout: VACUUM may rewrite a large file on slow disks.
		vctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		stats, err := st.Vacuum(vctx)
		if err != nil {
			log.Printf("vacuum: %v", err)
			return
		}
		if metrics != nil {
			metrics.ObserveVacuum(stats.Duration)
		}
		log.Printf("vacuum: ok in %s (db=%dB→%dB, wal=%dB→%dB)",
			stats.Duration.Round(time.Millisecond),
			stats.DBSizeBefore, stats.DBSizeAfter,
			stats.WALSizeBefore, stats.WALSizeAfter)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			doVacuum()
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// computeBuildID returns a deterministic 16-character hex digest computed
// over every embedded file's path and content. Two builds with the same
// embedded assets produce the same id; any change to even a single static
// asset (or template) produces a different one. Sorting the paths makes
// the walk order irrelevant to the result.
func computeBuildID(efs embed.FS) (string, error) {
	var paths []string
	if err := fs.WalkDir(efs, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		b, err := fs.ReadFile(efs, p)
		if err != nil {
			return "", err
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}
