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

// M5 auth gate + audit (Plan F6), extended by M5-AD with the
// directory login method. The cookie is HttpOnly and SameSite=Lax;
// it is deliberately NOT Secure because the panel is served over
// plain HTTP on the company LAN (Plan F6 recommends firewalling the
// panel port to the LAN, not TLS on it). A login over any other
// network is out of the panel's threat model — and the plan says
// so, rather than pretending otherwise.

// authMiddleware requires a valid session cookie for every API call
// except the auth endpoints themselves. The UI shell (GET /) and
// static assets stay public so the login form can load; they carry
// no data. When no auth service is attached (tests, degraded mode)
// the gate is open — production wiring always attaches one.
// Validation slides the session's idle deadline (M5-AD: 10 minutes
// from the last request), and the cookie is re-issued with the same
// MaxAge so browser and server agree on when it ends.
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
		token := sessionToken(r)
		if _, ok := auth.ValidateSession(token); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "belum login — buka panel dan login sebagai admin, atau jalankan: panel login",
				"auth_required": true,
			})
			return
		}
		refreshSessionCookie(w, token)
		next.ServeHTTP(w, r)
	})
}

func refreshSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     core.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(core.SessionIdleTimeout / time.Second),
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
// the request it records. The actor is the session's identity
// (local:admin or ad:<username>), never a fixed string.
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
		if auth := application.Auth(); auth == nil {
			actor = "anonymous"
		} else if a, ok := auth.ValidateSession(sessionToken(r)); ok {
			actor = a
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
	Method   string `json:"method"`   // "local" (default) or "ad"
	Username string `json:"username"` // AD method only
	Password string `json:"password"`
}

func (a *api) authStatus(w http.ResponseWriter, r *http.Request) {
	auth := a.application.Auth()
	if auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "authenticated": false, "ad_enabled": false})
		return
	}
	_, adCfg, _ := a.application.AD()
	actor, authenticated := auth.ValidateSession(sessionToken(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":    auth.Configured(),
		"authenticated": authenticated,
		"ad_enabled":    adCfg.Enabled,
		"actor":         actor,
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
	method := req.Method
	if method == "" {
		method = "local"
	}
	switch method {
	case "local":
		a.localLogin(w, r, auth, req.Password)
	case "ad":
		a.adLogin(w, r, auth, req.Username, req.Password)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "metode login tidak dikenal — pakai 'local' atau 'ad'"})
	}
}

func (a *api) localLogin(w http.ResponseWriter, r *http.Request, auth *core.AuthService, password string) {
	token, err := auth.Authenticate(password)
	if err != nil {
		_ = a.application.Audit().Append(core.AuditEntry{Actor: "anonymous", Action: "POST /api/v1/auth/login", Detail: "failed"})
		reason := "wrong-password"
		if errors.Is(err, core.ErrAdminNotConfigured) {
			reason = "admin-not-configured"
		}
		a.logWarn("login failed", "method", "local", "reason", reason)
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
	refreshSessionCookie(w, token)
	_ = a.application.Audit().Append(core.AuditEntry{Actor: core.LocalActor, Action: "POST /api/v1/auth/login", Detail: "ok"})
	a.logInfo("login ok", "method", "local", "actor", core.LocalActor)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "actor": core.LocalActor})
}

func (a *api) adLogin(w http.ResponseWriter, r *http.Request, auth *core.AuthService, username, password string) {
	directory, adCfg, adErr := a.application.AD()
	if !adCfg.Enabled || directory == nil {
		_ = a.application.Audit().Append(core.AuditEntry{Actor: "anonymous", Action: "POST /api/v1/auth/login", Detail: "failed (ad disabled)"})
		if adErr != nil {
			a.logWarn("login failed", "method", "ad", "username", username, "reason", "ad-disabled", "detail", adErr.Error())
		} else {
			a.logWarn("login failed", "method", "ad", "username", username, "reason", "ad-disabled")
		}
		msg := "login AD belum diaktifkan — aktifkan lewat panel.json (ad_enabled)"
		if adErr != nil {
			msg = "login AD belum bisa dipakai: " + adErr.Error()
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": msg})
		return
	}
	ok, err := directory.Authenticate(r.Context(), username, password)
	if err != nil {
		// Directory trouble is answered generically too: the login
		// form must not leak whether the DC is down or the account
		// is wrong. The detail goes to the panel log only.
		a.logWarn("login failed", "method", "ad", "username", username, "reason", "directory-error", "err", err)
		_ = a.application.Audit().Append(core.AuditEntry{Actor: "anonymous", Action: "POST /api/v1/auth/login", Detail: "failed (ad)"})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "username/password salah atau bukan anggota grup IT"})
		return
	}
	if !ok {
		a.logWarn("login failed", "method", "ad", "username", username, "reason", "wrong-credentials-or-not-member")
		_ = a.application.Audit().Append(core.AuditEntry{Actor: "anonymous", Action: "POST /api/v1/auth/login", Detail: "failed (ad)"})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "username/password salah atau bukan anggota grup IT"})
		return
	}
	actor := "ad:" + username
	token, err := auth.CreateSession(actor)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "gagal membuat sesi"})
		return
	}
	refreshSessionCookie(w, token)
	_ = a.application.Audit().Append(core.AuditEntry{Actor: actor, Action: "POST /api/v1/auth/login", Detail: "ok"})
	a.logInfo("login ok", "method", "ad", "actor", actor)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "actor": actor})
}

func (a *api) authLogout(w http.ResponseWriter, r *http.Request) {
	actor := "anonymous"
	if auth := a.application.Auth(); auth != nil {
		if act, ok := auth.ValidateSession(sessionToken(r)); ok {
			actor = act
		}
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
	_ = a.application.Audit().Append(core.AuditEntry{Actor: actor, Action: "POST /api/v1/auth/logout", Detail: "ok"})
	a.logInfo("logout", "actor", actor)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}
