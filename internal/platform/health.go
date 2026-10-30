package platform

import (
	"io"
	"net/http"
	"time"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// HTTPHealthChecker is the production core.HealthChecker: a URL is
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
func (h HTTPHealthChecker) Check(url string) core.HealthResult {
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
		return core.HealthResult{Err: err}
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return core.HealthResult{Err: err}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return core.HealthResult{OK: resp.StatusCode < 400, Status: resp.StatusCode}
}

var _ core.HealthChecker = HTTPHealthChecker{}
