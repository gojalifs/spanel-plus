// Package httpapi is the panel's HTTP face: JSON API plus the embedded
// web UI. Handlers are deliberately thin — they translate HTTP into
// calls on app.Application and core services, and hold no business
// logic of their own (Plan 10.1).
package httpapi

import (
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/buildinfo"
)

//go:embed web
var webFS embed.FS

// NewHandler builds the panel's HTTP routing.
func NewHandler(application *app.Application, logger *slog.Logger) http.Handler {
	index := template.Must(template.ParseFS(webFS, "web/index.html"))

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, application.Status())
	})

	mux.HandleFunc("GET /api/v1/doctor", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, application.Doctor(r.Context()))
	})

	// M3 state API (projects, PHP versions, Apache actions).
	(&api{application: application, logger: logger}).register(mux)

	staticFS, err := fs.Sub(webFS, "web/static")
	if err != nil {
		panic(err) // embedded path is a build-time constant
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := map[string]any{
			"Name":    buildinfo.Name,
			"Version": buildinfo.Version,
			"Port":    application.Status().PanelPort,
		}
		if err := index.Execute(w, data); err != nil {
			logger.Error("render index", "err", err)
		}
	})

	// Unknown API paths get a JSON 404 (not the HTML UI), so API
	// clients can tell a wrong URL apart from a dead panel.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})

	// M5: activity outside, audit inside it, auth gate inside that —
	// unauthenticated attempts are rejected before they reach a
	// handler but still leave an activity line; authenticated state
	// changes are recorded in the audit log as well.
	return activityMiddleware(application, logger, auditMiddleware(application, logger, authMiddleware(application, mux)))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
