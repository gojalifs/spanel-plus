package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/core"
)

// M5 auth gate + audit (Plan F6). The cookie is HttpOnly and
// SameSite=Lax; it is deliberately NOT Secure because the panel is
// served over plain HTTP on the company LAN (Plan F6 recommends
// firewalling the panel port to the LAN, not TLS on it). A login
// over any other network is out of the panel's threat model — and
// the plan says so, rather than pretending otherwise.

// authMiddleware requires a valid session cookie for every API call
// except the auth endpoints themselves. The UI shell (GET /) and
// static assets stay public so the login form can load; they carry
// no data. When no auth service is attached (tests, degraded mode)
// the gate is open — production wiring always attaches one.
func authMiddleware(application *app.Application, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := application.Auth()
		if auth == nil {
			next.ServeHTTP(w, r)
			return
		}
		if isPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if !auth.ValidateSession(sessionToken(r)) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "belum login — buka panel dan login sebagai admin, atau jalankan: panel login",
				"auth_required": true,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isPublicPath(r *http.Request) bool {
	if r.URL.Path == "/healthz" || r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/static/") {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/v1/auth/")
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(core.SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// statusWriter captures the response code for the audit log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// auditMiddleware records every state-changing API call (Plan F6:
// who, when, what). Reads are not logged. Login/logout are logged
// by their own handlers, with their outcome, so they are excluded
// here. A failed append is logged to the panel log but never fails
// the request it records.
func auditMiddleware(application *app.Application, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1/") ||
			strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		actor := "anonymous"
		if application.Auth() == nil || application.Auth().ValidateSession(sessionToken(r)) {
			actor = "admin"
		}
		err := application.Audit().Append(core.AuditEntry{
			Actor:  actor,
			Action: r.Method + " " + r.URL.Path,
			Detail: "status " + http.StatusText(sw.status),
		})
		if err != nil {
			logger.Warn("audit append failed", "err", err)
		}
	})
}

type loginRequest struct {
	Password string `json:"password"`
}

func (a *api) authStatus(w http.ResponseWriter, r *http.Request) {
	auth := a.application.Auth()
	if auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":    auth.Configured(),
		"authenticated": auth.ValidateSession(sessionToken(r)),
	})
}

func (a *api) authLogin(w http.ResponseWriter, r *http.Request) {
	auth := a.application.Auth()
	if auth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "layanan auth tidak tersedia di server ini"})
		return
	}
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	token, err := auth.Authenticate(req.Password)
	if err != nil {
		_ = a.application.Audit().Append(core.AuditEntry{Actor: "anonymous", Action: "POST /api/v1/auth/login", Detail: "failed"})
		switch {
		case errors.Is(err, core.ErrAdminNotConfigured):
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "akun admin belum dibuat — di server, jalankan: panel admin set-password",
			})
		default:
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "password admin salah"})
		}
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     core.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((12 * time.Hour) / time.Second),
	})
	_ = a.application.Audit().Append(core.AuditEntry{Actor: "admin", Action: "POST /api/v1/auth/login", Detail: "ok"})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

func (a *api) authLogout(w http.ResponseWriter, r *http.Request) {
	if auth := a.application.Auth(); auth != nil {
		auth.Logout(sessionToken(r))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     core.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	_ = a.application.Audit().Append(core.AuditEntry{Actor: "admin", Action: "POST /api/v1/auth/logout", Detail: "ok"})
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}
