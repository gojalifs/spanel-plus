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

// Check probes url once.
func (h HTTPHealthChecker) Check(url string) bool {
	timeout := h.Timeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode < 400
}

var _ core.HealthChecker = HTTPHealthChecker{}
