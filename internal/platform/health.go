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
	Timeout time.Duration // per-check timeout; default 2s when zero
}

// Check probes url once and reports what it saw: the HTTP status when
// the server answered, or the transport error when it did not.
func (h HTTPHealthChecker) Check(url string) core.HealthResult {
	timeout := h.Timeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return core.HealthResult{Err: err}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return core.HealthResult{OK: resp.StatusCode < 400, Status: resp.StatusCode}
}

var _ core.HealthChecker = HTTPHealthChecker{}
