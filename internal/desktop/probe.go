package desktop

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// HealthCheckNow performs one GET against the panel's /healthz and
// reports whether a healthy panel answered.
func HealthCheckNow(url string) bool {
	client := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := client.Get(url + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
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
