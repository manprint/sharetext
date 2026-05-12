package main

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"sharetext/internal/handlers"
	"sharetext/internal/store"
	"sharetext/internal/telemetry"
	"sharetext/internal/version"
)

//go:embed all:templates all:static
var assets embed.FS

type pageData struct {
	Slug string
}

func main() {
	cfg := loadConfigFromEnv(os.Getenv)
	handlers.MaxFileSize = cfg.MaxFileSize
	handlers.MaxContentSize = cfg.MaxContentSize
	metrics := telemetry.NewMetrics(cfg.MetricsEnabled)

	st, err := store.OpenWithOptions(cfg.DBPath, cfg.storeOptions())
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	api := &handlers.API{Store: st, Hub: handlers.NewHub(), SlugLen: cfg.SlugLen, Metrics: metrics, AuditLogDefaultLimit: cfg.AuditLogDefaultLimit}

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

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(cfg.RequestTimeout))
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

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	r.Group(func(g chi.Router) {
		g.Use(publicRateLimit)
		g.Get("/", func(w http.ResponseWriter, r *http.Request) {
			if err := tpl.ExecuteTemplate(w, "index.html", nil); err != nil {
				log.Printf("render index: %v", err)
			}
		})
		g.Get("/s/{slug}", func(w http.ResponseWriter, r *http.Request) {
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
			if err := tpl.ExecuteTemplate(w, "session.html", pageData{Slug: slug}); err != nil {
				log.Printf("render session: %v", err)
			}
		})
		g.Post("/api/sessions", api.CreateSession)
		g.Get("/api/sessions/{slug}", api.GetSession)
		g.Put("/api/sessions/{slug}", api.UpdateSession)
		g.Get("/ws/{slug}", api.WS)
		g.Post("/api/sessions/{slug}/files", api.UploadFile)
		g.Get("/api/sessions/{slug}/files", api.ListFiles)
		g.Get("/api/sessions/{slug}/files/{id}", api.DownloadFile)
		g.Get("/api/sessions/{slug}/bundle", api.Bundle)
	})

	adminAuth := handlers.BasicAuth(cfg.AdminUser, cfg.AdminPass, "sharetext-admin")
	r.Group(func(g chi.Router) {
		g.Use(adminRateLimit)
		g.Use(adminAuth)
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

	go runCleanup(ctx, st, cfg.CleanupInterval, cfg.FileGrace, metrics)
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
		log.Printf("sharetext %s listening on :%s (db=%s, cleanup=%s, file_grace=%s, vacuum=%s, admin=%s, max_file=%dB, max_content=%dB, file_backend=%s)", version.Version, cfg.Port, cfg.DBPath, cfg.CleanupInterval, cfg.FileGrace, vacuumMode, adminMode, handlers.MaxFileSize, handlers.MaxContentSize, cfg.FileStorageBackend)
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

func runCleanup(ctx context.Context, st *store.Store, every, fileGrace time.Duration, metrics *telemetry.Metrics) {
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
		if n, err := st.DeleteOrphanFiles(cctx, fileGrace); err != nil {
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
