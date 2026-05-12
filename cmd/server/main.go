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
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"sharetext/internal/handlers"
	"sharetext/internal/store"
	"sharetext/internal/version"
)

//go:embed all:templates all:static
var assets embed.FS

type pageData struct {
	Slug string
}

func main() {
	port := envOr("PORT", "8080")
	dbPath := envOr("DB_PATH", "sharetext.db")
	slugLen, _ := strconv.Atoi(envOr("SLUG_LEN", "16"))
	cleanupInterval, _ := time.ParseDuration(envOr("CLEANUP_INTERVAL", "30s"))
	if cleanupInterval <= 0 {
		cleanupInterval = 30 * time.Second
	}
	fileGrace, _ := time.ParseDuration(envOr("FILE_GRACE", "60s"))
	if fileGrace <= 0 {
		fileGrace = 60 * time.Second
	}
	// VACUUM_INTERVAL: 0 (or unset/invalid) disables the periodic vacuum job.
	vacuumInterval, _ := time.ParseDuration(envOr("VACUUM_INTERVAL", "0s"))
	if vacuumInterval < 0 {
		vacuumInterval = 0
	}
	adminUser := os.Getenv("ADMIN_USER")
	adminPass := os.Getenv("ADMIN_PASS")
	if v := os.Getenv("MAX_FILE_SIZE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			handlers.MaxFileSize = n
		} else {
			log.Printf("invalid MAX_FILE_SIZE %q, keeping default %d", v, handlers.MaxFileSize)
		}
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	api := &handlers.API{Store: st, Hub: handlers.NewHub(), SlugLen: slugLen}

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
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if err := tpl.ExecuteTemplate(w, "index.html", nil); err != nil {
			log.Printf("render index: %v", err)
		}
	})
	r.Get("/s/{slug}", func(w http.ResponseWriter, r *http.Request) {
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

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	r.Post("/api/sessions", api.CreateSession)
	r.Get("/api/sessions/{slug}", api.GetSession)
	r.Put("/api/sessions/{slug}", api.UpdateSession)
	r.Get("/ws/{slug}", api.WS)
	r.Post("/api/sessions/{slug}/files", api.UploadFile)
	r.Get("/api/sessions/{slug}/files", api.ListFiles)
	r.Get("/api/sessions/{slug}/files/{id}", api.DownloadFile)
	r.Get("/api/sessions/{slug}/bundle", api.Bundle)

	adminAuth := handlers.BasicAuth(adminUser, adminPass, "sharetext-admin")
	r.Group(func(g chi.Router) {
		g.Use(adminAuth)
		g.Get("/admin", func(w http.ResponseWriter, _ *http.Request) {
			if err := tpl.ExecuteTemplate(w, "admin.html", nil); err != nil {
				log.Printf("render admin: %v", err)
			}
		})
		g.Get("/admin/api/sessions", api.AdminList)
		g.Delete("/admin/api/sessions/{slug}", api.AdminDelete)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runCleanup(ctx, st, cleanupInterval, fileGrace)
	if vacuumInterval > 0 {
		go runVacuum(ctx, st, vacuumInterval)
	}

	go func() {
		adminMode := "disabled"
		if adminUser != "" && adminPass != "" {
			adminMode = "enabled (user=" + adminUser + ")"
		}
		vacuumMode := "disabled"
		if vacuumInterval > 0 {
			vacuumMode = vacuumInterval.String()
		}
		log.Printf("sharetext %s listening on :%s (db=%s, cleanup=%s, file_grace=%s, vacuum=%s, admin=%s, max_file=%dB)", version.Version, port, dbPath, cleanupInterval, fileGrace, vacuumMode, adminMode, handlers.MaxFileSize)
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

func runCleanup(ctx context.Context, st *store.Store, every, fileGrace time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	doSweep := func() {
		cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if n, err := st.DeleteExpired(cctx); err != nil {
			log.Printf("cleanup sessions: %v", err)
		} else if n > 0 {
			log.Printf("cleanup: deleted %d expired session(s)", n)
		}
		if n, err := st.DeleteOrphanFiles(cctx, fileGrace); err != nil {
			log.Printf("cleanup files: %v", err)
		} else if n > 0 {
			log.Printf("cleanup: deleted %d orphan file(s)", n)
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

func runVacuum(ctx context.Context, st *store.Store, every time.Duration) {
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
