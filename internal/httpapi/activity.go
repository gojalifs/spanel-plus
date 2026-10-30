package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/logging"
)

// Activity logging: every /api/ request leaves one structured line in
// panel.log — method, path, actor, status, duration, request
// parameters, and the error text when the status is an error. This is
// the log the operator reads when an apply fails: it shows what was
// asked for and what the panel answered, without needing to reproduce
// the click. Static UI traffic is not logged (it would drown the
// activity in asset fetches).

// secretKeys are request-body keys whose values must never reach the
// log, even though the parameter itself is worth recording. Login
// bodies carry "password" by design; the log shows that a password was
// supplied, never its value.
var secretKeys = map[string]bool{
	"password": true, "passwd": true, "token": true, "session": true,
	"secret": true, "authorization": true, "api_key": true, "apikey": true,
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if secretKeys[strings.ToLower(k)] {
				out[k] = "[redacted]"
			} else {
				out[k] = redactValue(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactValue(val)
		}
		return out
	default:
		return v
	}
}

// captureWriter records the status and a bounded copy of the response
// body so the middleware can log the error text of failed requests.
type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *captureWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	if w.body.Len() < 4096 {
		w.body.Write(b[:min(len(b), 4096-w.body.Len())])
	}
	return w.ResponseWriter.Write(b)
}

// errorFromBody extracts the {"error": "..."} message when present,
// else the raw body trimmed — the text the operator saw in the UI.
func errorFromBody(body []byte) string {
	var m map[string]any
	if json.Unmarshal(body, &m) == nil {
		if msg, ok := m["error"].(string); ok && msg != "" {
			return msg
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

func activityActor(application *app.Application, r *http.Request) string {
	auth := application.Auth()
	if auth == nil {
		return "anonymous"
	}
	if actor, ok := auth.ValidateSession(sessionToken(r)); ok {
		return actor
	}
	return "anonymous"
}

func activityMiddleware(application *app.Application, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		var params any
		if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
			raw, _ := io.ReadAll(io.LimitReader(r.Body, 32*1024))
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if len(raw) > 0 {
				var m map[string]any
				if json.Unmarshal(raw, &m) == nil {
					params = redactValue(m)
				} else {
					params = "unparsed"
				}
			}
		}
		cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r)
		attrs := []any{
			"method", r.Method, "path", r.URL.Path,
			"actor", activityActor(application, r),
			"status", cw.status, "duration_ms", time.Since(start).Milliseconds(),
		}
		if r.URL.RawQuery != "" {
			attrs = append(attrs, "query", r.URL.RawQuery)
		}
		if params != nil {
			attrs = append(attrs, "params", params)
		}
		if cw.status >= 400 {
			attrs = append(attrs, "error", errorFromBody(cw.body.Bytes()))
		}
		logger.Info("api request", attrs...)
	})
}

// panelLog serves the tail of the panel's own activity log for the
// dashboard's Log view. The file may not exist yet on a fresh panel;
// that is an empty log, not an error.
func (a *api) panelLog(w http.ResponseWriter, r *http.Request) {
	lines := 200
	if q := r.URL.Query().Get("lines"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n >= 1 && n <= 1000 {
			lines = n
		}
	}
	path := filepath.Join(a.application.LogDir(), logging.FileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"path": path, "total": 0, "lines": []string{}})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "baca log gagal: " + err.Error()})
		return
	}
	var all []string
	if len(raw) > 0 {
		all = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	}
	total := len(all)
	if total > lines {
		all = all[total-lines:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "total": total, "lines": all})
}
