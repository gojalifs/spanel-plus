package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
	"github.com/gojalifs/spanel-plus/internal/platform"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	cfg := config.Defaults(t.TempDir())
	cfg.DataDir = t.TempDir() // writable, so doctor's data-dir check passes
	doctor := core.Doctor{
		Runner: platform.ExecRunner{},
		Files:  platform.OSFileProbe{},
		Ports:  platform.OSPortProbe{},
	}
	application := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), doctor, platform.RealClock{})
	return NewHandler(application, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := get(t, testHandler(t), "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["status"] != "ok" {
		t.Fatalf("body = %s, err = %v", rec.Body.String(), err)
	}
}

func TestIndexServesEmbeddedUI(t *testing.T) {
	rec := get(t, testHandler(t), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// html/template escapes "+" as &#43; in the rendered page.
	if body := rec.Body.String(); !strings.Contains(body, "SPanel") || !strings.Contains(body, "Admin Panel") {
		t.Errorf("index must render the embedded UI, got: %.200s", body)
	}
}

func TestStatusEndpoint(t *testing.T) {
	rec := get(t, testHandler(t), "/api/v1/status")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("status endpoint must return JSON: %v", err)
	}
	if body["name"] != "SPanel+" || body["version"] == "" {
		t.Errorf("unexpected status body: %v", body)
	}
}

func TestDoctorEndpointReturnsReport(t *testing.T) {
	rec := get(t, testHandler(t), "/api/v1/doctor")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var report core.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("doctor endpoint must return a Report JSON: %v", err)
	}
	if len(report.Checks) == 0 {
		t.Error("doctor report must contain checks (failures included), never an empty success")
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	rec := get(t, testHandler(t), "/api/v1/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("API 404 must be JSON, got Content-Type %q", ct)
	}
}

func TestStaticAssetsServed(t *testing.T) {
	rec := get(t, testHandler(t), "/static/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "loadStatus") {
		t.Errorf("app.js must be the embedded asset, got: %.120s", rec.Body.String())
	}
}
