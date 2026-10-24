package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// M5: the CLI is a thin HTTP client of the running panel (Plan F7 &
// 10.1 — satu otak, dua muka). This is what makes `process
// start/stop` possible from a separate process at all: the supervisor
// lives in the panel, so the CLI must talk to it, not re-implement
// it. Consequence, stated plainly: state commands need the panel
// running (panel serve / panel service start) and a login session
// (`panel login` after `panel admin set-password`). `doctor`,
// `service`, `serve`, `app` and `admin` stay local — diagnostics and
// lifecycle must work precisely when the panel is down.

// ErrNeedLogin marks a 401 from the panel: no session, or an expired
// one. The CLI turns it into "jalankan panel login dulu".
var ErrNeedLogin = errors.New("panel session missing or expired")

// ErrPanelDown marks a panel that does not answer at all.
var ErrPanelDown = errors.New("panel does not answer")

// APIError is a non-2xx answer from the panel, carrying the server's
// own message (which already names the failing stage, e.g. an apply
// rollback) instead of a generic "request failed".
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("panel menjawab HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("panel menjawab HTTP %d", e.Status)
}

// TokenStore persists one CLI session token on the local machine
// (same trust boundary as admin set-password: a local operator can
// already read the data dir).
type TokenStore interface {
	Load() (string, error)
	Save(token string) error
	Clear() error
}

// FileTokenStore keeps the token in one 0600 file.
type FileTokenStore struct{ Path string }

func (s FileTokenStore) Load() (string, error) {
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func (s FileTokenStore) Save(token string) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.Path, []byte(token+"\n"), 0o600)
}

func (s FileTokenStore) Clear() error {
	if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// APIClient is every panel operation the CLI can perform. The HTTP
// implementation talks to a running panel; tests substitute a fake.
type APIClient interface {
	ListProjects() (core.Registry, error)
	AddProject(p core.Project) (core.ApplyResult, error)
	UpdateProject(name string, p core.Project) (core.ApplyResult, error)
	RemoveProject(name string) (core.ApplyResult, error)
	SetPHP(name, version string) (core.ApplyResult, error)
	SetEnabled(name string, enabled bool) (core.ApplyResult, error)
	PreviewSetPHP(name, version string) (core.ConfigDiff, error)
	ProjectConfig(name string) (string, error)
	ProjectWarnings(name string) ([]core.Warning, error)
	RegisterPHP(path string) (core.PHPRegistration, error)
	RemovePHP(version string) error
	PHPModules(version string) ([]string, error)
	ApacheStatus() (string, error)
	ApacheTest() error
	ApacheRestart() error
	ProcessStatuses() ([]core.ProcessStatus, error)
	ProcessStart(project, name string) error
	ProcessStop(project, name string) error
	ProcessRestart(project, name string) error
	ProcessLog(project, name string) (string, error)
	RunOnce(project, command string) (string, int, error)
	ScheduleRun(project string) (string, int, error)
	Login(password string) error
	Logout() error
}

// httpAPIClient is the production APIClient.
type httpAPIClient struct {
	base   string // e.g. http://127.0.0.1:8888
	tokens TokenStore
	http   *http.Client
}

// NewHTTPAPIClient builds a client for one panel address. A nil HTTP
// client gets a 120s timeout — an apply (restart + health checks)
// legitimately takes seconds, a hung panel must not hang the CLI.
func NewHTTPAPIClient(base string, tokens TokenStore, httpClient *http.Client) APIClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 120 * time.Second}
	}
	return &httpAPIClient{base: strings.TrimSuffix(base, "/"), tokens: tokens, http: httpClient}
}

func (c *httpAPIClient) request(method, path string, body any) (int, []byte, http.Header, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return 0, nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token, err := c.tokens.Load(); err == nil && token != "" {
		req.AddCookie(&http.Cookie{Name: core.SessionCookieName, Value: token})
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("panel tidak menjawab di %s — jalankan panel dulu (panel serve / panel service start), lalu ulangi: %w", c.base, ErrPanelDown)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return resp.StatusCode, data, resp.Header, ErrNeedLogin
	}
	if resp.StatusCode >= 400 {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &msg)
		if msg.Error == "" {
			msg.Error = strings.TrimSpace(string(data))
		}
		return resp.StatusCode, data, resp.Header, &APIError{Status: resp.StatusCode, Message: msg.Error}
	}
	return resp.StatusCode, data, resp.Header, nil
}

func (c *httpAPIClient) call(method, path string, body any, out any) error {
	_, data, _, err := c.request(method, path, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("panel menjawab JSON yang tidak dikenali: %w", err)
	}
	return nil
}

type applyResponse struct {
	Apply core.ApplyResult `json:"apply"`
}

func (c *httpAPIClient) applyCall(method, path string, body any) (core.ApplyResult, error) {
	var res applyResponse
	if err := c.call(method, path, body, &res); err != nil {
		return core.ApplyResult{}, err
	}
	return res.Apply, nil
}

func (c *httpAPIClient) ListProjects() (core.Registry, error) {
	var reg core.Registry
	err := c.call(http.MethodGet, "/api/v1/projects", nil, &reg)
	return reg, err
}

func (c *httpAPIClient) AddProject(p core.Project) (core.ApplyResult, error) {
	return c.applyCall(http.MethodPost, "/api/v1/projects", p)
}

func (c *httpAPIClient) UpdateProject(name string, p core.Project) (core.ApplyResult, error) {
	return c.applyCall(http.MethodPut, "/api/v1/projects/"+name, p)
}

func (c *httpAPIClient) RemoveProject(name string) (core.ApplyResult, error) {
	return c.applyCall(http.MethodDelete, "/api/v1/projects/"+name, nil)
}

func (c *httpAPIClient) SetPHP(name, version string) (core.ApplyResult, error) {
	return c.applyCall(http.MethodPost, "/api/v1/projects/"+name+"/set-php", map[string]string{"version": version})
}

func (c *httpAPIClient) SetEnabled(name string, enabled bool) (core.ApplyResult, error) {
	return c.applyCall(http.MethodPost, "/api/v1/projects/"+name+"/enabled", map[string]bool{"enabled": enabled})
}

func (c *httpAPIClient) PreviewSetPHP(name, version string) (core.ConfigDiff, error) {
	var diff core.ConfigDiff
	err := c.call(http.MethodPost, "/api/v1/projects/"+name+"/preview-set-php", map[string]string{"version": version}, &diff)
	return diff, err
}

func (c *httpAPIClient) ProjectConfig(name string) (string, error) {
	var res struct {
		Config string `json:"config"`
	}
	err := c.call(http.MethodGet, "/api/v1/projects/"+name+"/config", nil, &res)
	return res.Config, err
}

func (c *httpAPIClient) ProjectWarnings(name string) ([]core.Warning, error) {
	var res struct {
		Warnings []core.Warning `json:"warnings"`
	}
	err := c.call(http.MethodGet, "/api/v1/projects/"+name+"/warnings", nil, &res)
	if res.Warnings == nil {
		res.Warnings = []core.Warning{}
	}
	return res.Warnings, err
}

func (c *httpAPIClient) RegisterPHP(path string) (core.PHPRegistration, error) {
	var reg core.PHPRegistration
	err := c.call(http.MethodPost, "/api/v1/php", map[string]string{"path": path}, &reg)
	return reg, err
}

func (c *httpAPIClient) RemovePHP(version string) error {
	return c.call(http.MethodDelete, "/api/v1/php/"+version, nil, nil)
}

func (c *httpAPIClient) PHPModules(version string) ([]string, error) {
	var res struct {
		Modules []string `json:"modules"`
	}
	err := c.call(http.MethodGet, "/api/v1/php/"+version+"/modules", nil, &res)
	return res.Modules, err
}

func (c *httpAPIClient) ApacheStatus() (string, error) {
	var res struct {
		Status string `json:"status"`
	}
	err := c.call(http.MethodGet, "/api/v1/apache/status", nil, &res)
	return res.Status, err
}

func (c *httpAPIClient) ApacheTest() error {
	return c.call(http.MethodPost, "/api/v1/apache/test", nil, nil)
}

func (c *httpAPIClient) ApacheRestart() error {
	return c.call(http.MethodPost, "/api/v1/apache/restart", nil, nil)
}

func (c *httpAPIClient) ProcessStatuses() ([]core.ProcessStatus, error) {
	var res struct {
		Processes []core.ProcessStatus `json:"processes"`
	}
	err := c.call(http.MethodGet, "/api/v1/processes", nil, &res)
	if res.Processes == nil {
		res.Processes = []core.ProcessStatus{}
	}
	return res.Processes, err
}

func (c *httpAPIClient) ProcessStart(project, name string) error {
	return c.call(http.MethodPost, "/api/v1/projects/"+project+"/processes/"+name+"/start", nil, nil)
}

func (c *httpAPIClient) ProcessStop(project, name string) error {
	return c.call(http.MethodPost, "/api/v1/projects/"+project+"/processes/"+name+"/stop", nil, nil)
}

func (c *httpAPIClient) ProcessRestart(project, name string) error {
	return c.call(http.MethodPost, "/api/v1/projects/"+project+"/processes/"+name+"/restart", nil, nil)
}

func (c *httpAPIClient) ProcessLog(project, name string) (string, error) {
	var res struct {
		Log string `json:"log"`
	}
	err := c.call(http.MethodGet, "/api/v1/projects/"+project+"/processes/"+name+"/log", nil, &res)
	return res.Log, err
}

func (c *httpAPIClient) RunOnce(project, command string) (string, int, error) {
	var res struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
	}
	err := c.call(http.MethodPost, "/api/v1/projects/"+project+"/run", map[string]string{"command": command}, &res)
	return res.Output, res.ExitCode, err
}

func (c *httpAPIClient) ScheduleRun(project string) (string, int, error) {
	var res struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
	}
	err := c.call(http.MethodPost, "/api/v1/projects/"+project+"/schedule-run", nil, &res)
	return res.Output, res.ExitCode, err
}

func (c *httpAPIClient) Login(password string) error {
	raw, _ := json.Marshal(map[string]string{"password": password})
	req, err := http.NewRequest(http.MethodPost, c.base+"/api/v1/auth/login", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("panel tidak menjawab di %s — jalankan panel dulu (panel serve / panel service start), lalu ulangi: %w", c.base, ErrPanelDown)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrNeedLogin
	}
	if resp.StatusCode >= 400 {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &msg)
		if msg.Error == "" {
			msg.Error = strings.TrimSpace(string(data))
		}
		return &APIError{Status: resp.StatusCode, Message: msg.Error}
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == core.SessionCookieName && ck.Value != "" {
			return c.tokens.Save(ck.Value)
		}
	}
	return fmt.Errorf("login berhasil tapi panel tidak mengirim sesi — coba lagi")
}

func (c *httpAPIClient) Logout() error {
	// Best effort against the server; the local token is cleared
	// regardless so a dead panel cannot leave a stale session file.
	_ = c.call(http.MethodPost, "/api/v1/auth/logout", nil, nil)
	return c.tokens.Clear()
}
