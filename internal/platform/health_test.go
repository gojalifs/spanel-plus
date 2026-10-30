package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPHealthCheckerReportsStatus(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	ng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ng.Close()

	h := HTTPHealthChecker{}
	if r := h.Check(ok.URL); !r.OK || r.Status != 200 {
		t.Errorf("200 must be OK with status 200, got %+v", r)
	}
	if r := h.Check(ng.URL); r.OK || r.Status != 404 {
		t.Errorf("404 must not be OK and must carry status 404, got %+v", r)
	}
}

func TestHTTPHealthCheckerReportsTransportError(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close() // nothing listens anymore: refused, not a status

	h := HTTPHealthChecker{}
	if r := h.Check(url); r.OK || r.Status != 0 || r.Err == nil {
		t.Errorf("refused probe must be !OK with no status and an error, got %+v", r)
	}
	if !strings.Contains(h.Check(url).Describe(), "refused") && !strings.Contains(h.Check(url).Describe(), "connect") {
		t.Errorf("describe must name the transport failure, got %q", h.Check(url).Describe())
	}
}

func TestHTTPHealthCheckerTimeoutIsAnErrorNotAStatus(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer slow.Close()

	h := HTTPHealthChecker{Timeout: 50 * time.Millisecond}
	if r := h.Check(slow.URL); r.OK || r.Status != 0 || r.Err == nil {
		t.Errorf("timed-out probe must be !OK with no status and an error, got %+v", r)
	}
}
