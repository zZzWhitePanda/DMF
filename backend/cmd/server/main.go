// Dank Memer Farm backend server.
//
// Serves:
//   - the Next.js dashboard (static files from ./dashboard-build/)
//   - the HTTP + WebSocket API
//   - Google OAuth sign-in
//
// Config comes from env vars (see the usage block below). On Fly.io the
// vars are set via `fly secrets set KEY=value`; locally, use a .env file
// loaded by your shell or `fly launch` scaffolding.
package main

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/zZzWhitePanda/DMF/backend/internal/api"
	"github.com/zZzWhitePanda/DMF/backend/internal/auth"
	"github.com/zZzWhitePanda/DMF/backend/internal/db"
	"github.com/zZzWhitePanda/DMF/backend/internal/runtime"
	"github.com/zZzWhitePanda/DMF/backend/internal/static"
)

func main() {
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true, TimestampFormat: time.RFC3339})
	log.SetLevel(log.InfoLevel)

	cfg := loadEnv()
	validateEnv(cfg)

	// Database — persisted on the Fly volume at /data/dmf.db.
	// Locally defaults to ./dmf.db in the current working dir.
	database, err := db.Open(cfg.DatabasePath)
	if err != nil {
		log.WithError(err).Fatal("open db failed")
	}
	defer database.Close()

	// Auth provider + middleware.
	authCfg := auth.Config{
		ClientID:        cfg.GoogleClientID,
		ClientSecret:    cfg.GoogleClientSecret,
		RedirectURL:     cfg.PublicURL + "/auth/google/callback",
		AdminEmail:      cfg.AdminEmail,
		SessionDuration: 30 * 24 * time.Hour,
		SessionCookie:   "dmf_session",
		SecureCookies:   strings.HasPrefix(cfg.PublicURL, "https://"),
	}
	provider := auth.NewGoogleProvider(authCfg)
	middleware := auth.NewMiddleware(provider, database)

	// Grinder runtime manager.
	manager := runtime.New()
	defer manager.StopAll()

	// Route tables. Public routes attach directly. Authenticated routes
	// go through a sub-mux wrapped by RequireAuth; licensed routes get
	// wrapped by RequireLicensed on top.
	root := http.NewServeMux()

	// Auth routes (public).
	authHandlers := api.NewAuthHandlers(provider, database, authCfg)
	authHandlers.Register(root)

	// License-management routes — auth required, but NOT license-gated
	// (users without a valid license need to reach the HWID-reset
	// endpoint to recover). Mounted FIRST so its /api/license/* paths
	// win over the licensed mux's catchall below.
	licenseMux := http.NewServeMux()
	licenseHandlers := api.NewLicenseHandlers(database)
	licenseHandlers.Register(licenseMux)
	root.Handle("/api/license/", middleware.RequireAuth(licenseMux))

	// Licensed API routes.
	licensedMux := http.NewServeMux()
	// New-style single-farm endpoints (what the dashboard uses).
	farmHandlers := api.NewFarmHandlers(database, manager)
	farmHandlers.Register(licensedMux)
	// Legacy multi-account endpoints (kept for future multi-grinder UI).
	accountHandlers := api.NewAccountHandlers(database, manager)
	accountHandlers.Register(licensedMux)
	// WebSocket (both the per-account /api/ws/{id} and /api/farm/ws live here).
	wsHandlers := api.NewWSHandlers(database, manager, []string{cfg.PublicURL})
	wsHandlers.Register(licensedMux)
	// RequireAuth → RequireLicensed → handlers.
	licensed := middleware.RequireAuth(middleware.RequireLicensed(licensedMux))
	root.Handle("/api/", licensed)

	// Dashboard static files. Embedded at build time. The server serves
	// index.html for all unmatched routes so Next.js's client-side
	// routing works (SPA fallback).
	dashboard := newDashboardHandler(static.Dashboard())
	root.Handle("/", dashboard)

	// Background: purge expired sessions every hour.
	go sessionPurger(database)

	// Graceful shutdown.
	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		// WebSocket connections hold the handler open indefinitely so
		// WriteTimeout is intentionally unset.
	}
	go func() {
		log.WithField("addr", server.Addr).Info("dmf server listening")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("server failed")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info("shutdown signal received, draining")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// env + dashboard static serving
// ---------------------------------------------------------------------------

type envConfig struct {
	Port               string
	PublicURL          string
	DatabasePath       string
	GoogleClientID     string
	GoogleClientSecret string
	AdminEmail         string
	MasterSecret       string // for admin key-gen endpoints (wired later)
}

func loadEnv() envConfig {
	return envConfig{
		Port:               getenv("PORT", "8080"),
		PublicURL:          getenv("PUBLIC_URL", "http://localhost:8080"),
		DatabasePath:       getenv("DATABASE_PATH", "./dmf.db"),
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		AdminEmail:         os.Getenv("ADMIN_EMAIL"),
		MasterSecret:       os.Getenv("MASTER_SECRET"),
	}
}

func validateEnv(c envConfig) {
	missing := []string{}
	if c.GoogleClientID == "" {
		missing = append(missing, "GOOGLE_CLIENT_ID")
	}
	if c.GoogleClientSecret == "" {
		missing = append(missing, "GOOGLE_CLIENT_SECRET")
	}
	if c.AdminEmail == "" {
		missing = append(missing, "ADMIN_EMAIL")
	}
	if len(missing) > 0 {
		log.Fatalf("missing required env vars: %s", strings.Join(missing, ", "))
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// newDashboardHandler returns an http.Handler that serves the embedded
// Next.js static export with SPA fallback — any route Next.js doesn't
// recognize gets index.html back so the client router can resolve it.
func newDashboardHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(fsys, p); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		routeIndex := filepath.ToSlash(filepath.Join(p, "index.html"))
		if _, err := fs.Stat(fsys, routeIndex); err == nil {
			r.URL.Path = "/" + routeIndex
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r)
	})
}

// sessionPurger runs forever, deleting expired sessions each hour.
func sessionPurger(d *db.DB) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for range t.C {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := d.PurgeExpiredSessions(ctx); err != nil {
			log.WithError(err).Debug("session purge failed")
		}
		cancel()
	}
}

