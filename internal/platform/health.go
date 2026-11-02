package platform

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// HealthResult is one probe's outcome. Status is the HTTP status when
// the server answered (0 otherwise); Err is the transport error when
// the server did not answer at all.
//
// NOTE (2026-11-02): this prober is no longer wired into the apply
// pipeline — applies never gate on app responses (operator decision:
// the gate kept failing on application-side refusals that are not
// config errors, e.g. a browser-version gate answering 426 or a team's
// auth filter answering 403, and rolled back correct configs). The
// prober stays as a tested standalone utility for future manual
// diagnostics (e.g. a per-project health path); it is not dead code
// with a caller to come, it is a tool with its tests still green.
type HealthResult struct {
	OK     bool
	Status int
	Err    error
}

// Describe renders the failure detail for logs and error messages:
// "status 404", the transport error, or "no response".
func (r HealthResult) Describe() string {
	if r.Status != 0 {
		return fmt.Sprintf("status %d", r.Status)
	}
	if r.Err != nil {
		return r.Err.Error()
	}
	return "no response"
}

// HealthChecker probes one URL and reports the outcome in detail.
type HealthChecker interface {
	Check(url string) HealthResult
}

// HTTPHealthChecker is the production HealthChecker: a URL is
// healthy when it answers with a status below 400 (redirects are
// followed — a Laravel app redirecting to its login page is alive).
type HTTPHealthChecker struct {
	Timeout   time.Duration // per-check timeout; default 2s when zero
	UserAgent string        // default HealthCheckUserAgent when empty
}

// HealthCheckUserAgent identifies the probe as a current Edge browser.
// Some apps gate on the client: siws-mobile_laravel's login answers
// 426 to any client whose browser version it cannot parse as >= 100
// (its browser-not-supported view), so a bare Go client ("Go-http-client")
// fails the health check even though the app is alive for every real
// user. The probe represents a supported client; real failures (500,
// DB down, refused, timeout) still fail the check exactly as before.
// Bump this when gated apps raise their minimum above Edge 140.
const HealthCheckUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0"

// Check probes url once and reports what it saw: the HTTP status when
// the server answered, or the transport error when it did not.
func (h HTTPHealthChecker) Check(url string) HealthResult {
	timeout := h.Timeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	ua := h.UserAgent
	if ua == "" {
		ua = HealthCheckUserAgent
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return HealthResult{Err: err}
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return HealthResult{Err: err}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return HealthResult{OK: resp.StatusCode < 400, Status: resp.StatusCode}
}

var _ HealthChecker = HTTPHealthChecker{}
