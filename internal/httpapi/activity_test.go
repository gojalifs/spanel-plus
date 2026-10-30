package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
	"github.com/gojalifs/spanel-plus/internal/platform"
)

// activityHandler mirrors apiHandler but captures the activity log
// lines in buf instead of discarding them.
func activityHandler(t *testing.T, buf *bytes.Buffer) http.Handler {
	t.Helper()
	cfg := config.Defaults(t.TempDir())
	cfg.DataDir = t.TempDir()
	doctor := core.Doctor{Runner: platform.ExecRunner{}, Files: platform.OSFileProbe{}, Ports: platform.OSPortProbe{}}
	application := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), doctor, platform.RealClock{})
	fp := &fakeProjects{reg: core.Registry{
		Settings:    core.Settings{ApacheService: "Apache2.4"},
		PHPVersions: []core.PHPVersion{{Version: "8.2", Path: `C:\php\8.2`}},
		Projects:    []core.Project{{Name: "app", URLPath: "/app", Type: core.ProjectLaravel, PHPVersion: "8.2", Enabled: true}},
	}}
	application.AttachServices(fp, &fakePHP{}, &fakeApache{})
	return NewHandler(application, slog.New(slog.NewJSONHandler(buf, nil)))
}

// activityLines parses the captured JSON log lines and returns the
// "api request" entries.
func activityLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line must be JSON: %v (%q)", err, line)
		}
		if m["msg"] == "api request" {
			out = append(out, m)
		}
	}
	return out
}

func TestActivityMiddlewareLogsParamsRedacted(t *testing.T) {
	var buf bytes.Buffer
	h := activityHandler(t, &buf)
	rec := call(t, h, "POST", "/api/v1/projects", `{"name":"app2","url_path":"/app2","type":"laravel","php_version":"8.2","password":"rahasia-jangan-muncul"}`)
	if rec.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(buf.String(), "rahasia-jangan-muncul") {
		t.Fatalf("password must never reach the log, got:\n%s", buf.String())
	}
	lines := activityLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("expected exactly one api request line, got %d:\n%s", len(lines), buf.String())
	}
	line := lines[0]
	if line["method"] != "POST" || line["path"] != "/api/v1/projects" || line["status"] != float64(201) {
		t.Errorf("log line must carry method/path/status, got %v", line)
	}
	params, ok := line["params"].(map[string]any)
	if !ok {
		t.Fatalf("log line must carry params object, got %v", line)
	}
	if params["name"] != "app2" {
		t.Errorf("params must include the project name, got %v", params)
	}
	if params["password"] != "[redacted]" {
		t.Errorf("password param must be redacted, got %v", params)
	}
}

func TestActivityMiddlewareLogsErrorText(t *testing.T) {
	var buf bytes.Buffer
	h := activityHandler(t, &buf)
	rec := call(t, h, "POST", "/api/v1/projects", `{bukan json`)
	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	lines := activityLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("expected exactly one api request line, got %d", len(lines))
	}
	line := lines[0]
	if line["status"] != float64(400) {
		t.Errorf("log line must carry status 400, got %v", line)
	}
	errText, _ := line["error"].(string)
	if !strings.Contains(errText, "tidak valid") {
		t.Errorf("error field must carry the handler's error text, got %q", errText)
	}
	if line["params"] != "unparsed" {
		t.Errorf("unparseable body must log params as unparsed, got %v", line["params"])
	}
}

func TestPanelLogEndpointServesTail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "panel.log"), []byte("satu\ndua\ntiga\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(t.TempDir())
	cfg.LogDir = dir
	doctor := core.Doctor{Runner: platform.ExecRunner{}, Files: platform.OSFileProbe{}, Ports: platform.OSPortProbe{}}
	application := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), doctor, platform.RealClock{})
	h := NewHandler(application, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := call(t, h, "GET", "/api/v1/logs", "")
	var full map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &full); err != nil {
		t.Fatalf("response must be JSON: %v (%s)", err, rec.Body.String())
	}
	if full["total"] != float64(3) {
		t.Errorf("expected total 3, got %v", full)
	}
	lines, _ := full["lines"].([]any)
	if len(lines) != 3 || lines[0] != "satu" || lines[2] != "tiga" {
		t.Errorf("expected all three lines in order, got %v", lines)
	}

	rec = call(t, h, "GET", "/api/v1/logs?lines=2", "")
	var tail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tail); err != nil {
		t.Fatal(err)
	}
	tailLines, _ := tail["lines"].([]any)
	if len(tailLines) != 2 || tailLines[0] != "dua" || tailLines[1] != "tiga" {
		t.Errorf("expected last two lines, got %v", tailLines)
	}
}

func TestPanelLogEndpointEmptyWhenMissing(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.LogDir = t.TempDir() // no panel.log inside
	doctor := core.Doctor{Runner: platform.ExecRunner{}, Files: platform.OSFileProbe{}, Ports: platform.OSPortProbe{}}
	application := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), doctor, platform.RealClock{})
	h := NewHandler(application, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := call(t, h, "GET", "/api/v1/logs", "")
	if rec.Code != 200 {
		t.Fatalf("missing log file must be an empty log, not an error: %d %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res["total"] != float64(0) {
		t.Errorf("expected total 0, got %v", res)
	}
}
