package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/core"
)

// The M3 state API: the UI's only path to changing anything (Plan
// 4.1 — satu otak, dua muka). Handlers stay thin: decode, call the
// service, map the error. All validation and the apply pipeline live
// in core.

type api struct {
	application *app.Application
}

func (a *api) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/overview", a.overview)
	mux.HandleFunc("GET /api/v1/projects", a.listProjects)
	mux.HandleFunc("POST /api/v1/projects", a.addProject)
	mux.HandleFunc("PUT /api/v1/projects/{name}", a.updateProject)
	mux.HandleFunc("DELETE /api/v1/projects/{name}", a.removeProject)
	mux.HandleFunc("POST /api/v1/projects/{name}/set-php", a.setPHP)
	mux.HandleFunc("POST /api/v1/projects/{name}/enabled", a.setEnabled)
	mux.HandleFunc("GET /api/v1/projects/{name}/config", a.projectConfig)
	mux.HandleFunc("POST /api/v1/projects/{name}/preview-set-php", a.previewSetPHP)
	mux.HandleFunc("GET /api/v1/projects/{name}/warnings", a.projectWarnings)
	mux.HandleFunc("POST /api/v1/php", a.registerPHP)
	mux.HandleFunc("DELETE /api/v1/php/{version}", a.removePHP)
	mux.HandleFunc("GET /api/v1/php/{version}/modules", a.phpModules)
	mux.HandleFunc("POST /api/v1/apache/restart", a.apacheRestart)
	mux.HandleFunc("POST /api/v1/apache/test", a.apacheTest)
}

// overview is the dashboard summary (F8): panel status, Apache
// service state, counts, and the registry contents.
func (a *api) overview(w http.ResponseWriter, _ *http.Request) {
	projects, _, apache := a.services(w)
	if projects == nil {
		return
	}
	reg, err := projects.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	apacheStatus := ""
	if apache != nil {
		apacheStatus, _ = apache.Status()
	}
	active := 0
	for _, p := range reg.Projects {
		if p.Enabled {
			active++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          a.application.Status(),
		"apache_status":   apacheStatus,
		"apache_service":  reg.Settings.ApacheService,
		"projects_total":  len(reg.Projects),
		"projects_active": active,
		"php_versions":    len(reg.PHPVersions),
	})
}

func (a *api) listProjects(w http.ResponseWriter, _ *http.Request) {
	projects, _, _ := a.services(w)
	if projects == nil {
		return
	}
	reg, err := projects.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	// The dashboard iterates these lists; empty must be [], not null.
	if reg.Projects == nil {
		reg.Projects = []core.Project{}
	}
	if reg.PHPVersions == nil {
		reg.PHPVersions = []core.PHPVersion{}
	}
	writeJSON(w, http.StatusOK, reg)
}

func (a *api) addProject(w http.ResponseWriter, r *http.Request) {
	projects, _, _ := a.services(w)
	if projects == nil {
		return
	}
	var p core.Project
	if !decodeJSON(w, r, &p) {
		return
	}
	res, err := projects.AddProject(r.Context(), p)
	writeApply(w, res, err, http.StatusCreated)
}

func (a *api) updateProject(w http.ResponseWriter, r *http.Request) {
	projects, _, _ := a.services(w)
	if projects == nil {
		return
	}
	var p core.Project
	if !decodeJSON(w, r, &p) {
		return
	}
	res, err := projects.UpdateProject(r.Context(), r.PathValue("name"), p)
	writeApply(w, res, err, http.StatusOK)
}

func (a *api) removeProject(w http.ResponseWriter, r *http.Request) {
	projects, _, _ := a.services(w)
	if projects == nil {
		return
	}
	res, err := projects.RemoveProject(r.Context(), r.PathValue("name"))
	writeApply(w, res, err, http.StatusOK)
}

type versionRequest struct {
	Version string `json:"version"`
}

func (a *api) setPHP(w http.ResponseWriter, r *http.Request) {
	projects, _, _ := a.services(w)
	if projects == nil {
		return
	}
	var req versionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := projects.SetPHP(r.Context(), r.PathValue("name"), req.Version)
	writeApply(w, res, err, http.StatusOK)
}

func (a *api) previewSetPHP(w http.ResponseWriter, r *http.Request) {
	projects, _, _ := a.services(w)
	if projects == nil {
		return
	}
	var req versionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	diff, err := projects.PreviewSetPHP(r.PathValue("name"), req.Version)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

type enabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (a *api) setEnabled(w http.ResponseWriter, r *http.Request) {
	projects, _, _ := a.services(w)
	if projects == nil {
		return
	}
	var req enabledRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := projects.SetEnabled(r.Context(), r.PathValue("name"), req.Enabled)
	writeApply(w, res, err, http.StatusOK)
}

func (a *api) projectConfig(w http.ResponseWriter, r *http.Request) {
	projects, _, _ := a.services(w)
	if projects == nil {
		return
	}
	content, err := projects.CurrentConfig(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"config": content})
}

func (a *api) projectWarnings(w http.ResponseWriter, r *http.Request) {
	projects, _, _ := a.services(w)
	if projects == nil {
		return
	}
	warnings, err := projects.Warnings(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if warnings == nil {
		warnings = []core.Warning{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"warnings": warnings})
}

type phpRegisterRequest struct {
	Path string `json:"path"`
}

func (a *api) registerPHP(w http.ResponseWriter, r *http.Request) {
	_, php, _ := a.services(w)
	if php == nil {
		return
	}
	var req phpRegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	reg, err := php.Register(r.Context(), req.Path)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, reg)
}

func (a *api) removePHP(w http.ResponseWriter, r *http.Request) {
	_, php, _ := a.services(w)
	if php == nil {
		return
	}
	if err := php.Remove(r.PathValue("version")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"removed": r.PathValue("version")})
}

func (a *api) phpModules(w http.ResponseWriter, r *http.Request) {
	_, php, _ := a.services(w)
	if php == nil {
		return
	}
	mods, err := php.Modules(r.Context(), r.PathValue("version"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": mods})
}

func (a *api) apacheRestart(w http.ResponseWriter, r *http.Request) {
	_, _, apache := a.services(w)
	if apache == nil {
		return
	}
	if err := apache.Restart(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"restarted": "ok"})
}

func (a *api) apacheTest(w http.ResponseWriter, r *http.Request) {
	_, _, apache := a.services(w)
	if apache == nil {
		return
	}
	if err := apache.TestConfig(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"config": "ok"})
}

// services returns the attached services, answering 503 when the
// panel runs without them (broken panel.json — status/doctor still
// work, state changes must not half-run).
func (a *api) services(w http.ResponseWriter) (app.ProjectOps, app.PHPOps, app.ApacheOps) {
	p, php, ap := a.application.Projects(), a.application.PHP(), a.application.Apache()
	if p == nil || php == nil || ap == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "layanan registry tidak tersedia — periksa panel.json (lihat log panel)",
		})
		return nil, nil, nil
	}
	return p, php, ap
}

func writeApply(w http.ResponseWriter, res core.ApplyResult, err error, okCode int) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, okCode, map[string]any{"apply": res})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body JSON tidak valid: " + err.Error()})
		return false
	}
	return true
}

// writeErr maps core failures onto HTTP status codes: unknown things
// are 404, state conflicts 409, a failed apply pipeline 502 (the
// message carries the stage and the rollback outcome), everything
// else is a validation-style 400.
func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, core.ErrProjectNotFound), errors.Is(err, core.ErrPHPVersionNotRegistered):
		code = http.StatusNotFound
	case errors.Is(err, core.ErrProjectExists), errors.Is(err, core.ErrPHPVersionExists), errors.Is(err, core.ErrPHPVersionInUse):
		code = http.StatusConflict
	case strings.Contains(err.Error(), "apply failed"), strings.Contains(err.Error(), "rolled back"):
		code = http.StatusBadGateway
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
