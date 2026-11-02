package desktop

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HealthCheckNow performs one GET against the panel's /healthz and
// reports whether a healthy panel answered. For an https loopback URL
// (a TLS-active panel), certificate verification is skipped: this is
// a liveness probe to the same machine, not authentication, and the
// panel's private CA is not in the system pool. Non-loopback https
// keeps full verification.
func HealthCheckNow(url string) bool {
	client := &http.Client{Timeout: 400 * time.Millisecond}
	if strings.HasPrefix(url, "https://") && isLoopbackURL(url) {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // loopback liveness only
		}
	}
	resp, err := client.Get(url + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// isLoopbackURL reports whether a URL's host is loopback.
func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// WaitReady polls /healthz until the panel answers or the attempt
// budget runs out. Poll errors are expected while the server starts;
// exhausting the budget is the loud failure.
func WaitReady(url string, attempts int, interval time.Duration) error {
	for i := 0; i < attempts; i++ {
		if HealthCheckNow(url) {
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("tidak ada jawaban dari %s/healthz setelah %d percobaan", url, attempts)
}
