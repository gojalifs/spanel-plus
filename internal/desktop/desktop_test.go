package desktop

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLaunchReusesRunningServerWithoutStoppingIt(t *testing.T) {
	started, stopped, opened := false, false, false
	l := Launcher{
		HealthCheck: func() bool { return true },
		StartServer: func() (func(), error) {
			started = true
			return func() { stopped = true }, nil
		},
		WaitReady:  func() error { return nil },
		OpenWindow: func() error { opened = true; return nil },
	}
	if err := l.Launch(); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if started {
		t.Error("server already running: StartServer must not be called")
	}
	if stopped {
		t.Error("closing the window must not stop a server owned by another process (the Windows Service)")
	}
	if !opened {
		t.Error("window must open")
	}
}

func TestLaunchStartsOwnServerAndStopsItOnClose(t *testing.T) {
	var order []string
	l := Launcher{
		HealthCheck: func() bool { return false },
		StartServer: func() (func(), error) {
			order = append(order, "start")
			return func() { order = append(order, "stop") }, nil
		},
		WaitReady:  func() error { order = append(order, "ready"); return nil },
		OpenWindow: func() error { order = append(order, "window"); return nil },
	}
	if err := l.Launch(); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	want := []string{"start", "ready", "window", "stop"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestLaunchWaitReadyFailureSkipsWindowAndStopsServer(t *testing.T) {
	stopped, opened := false, false
	l := Launcher{
		HealthCheck: func() bool { return false },
		StartServer: func() (func(), error) { return func() { stopped = true }, nil },
		WaitReady:   func() error { return errors.New("no answer") },
		OpenWindow:  func() error { opened = true; return nil },
	}
	if err := l.Launch(); err == nil {
		t.Fatal("WaitReady failure must fail Launch loudly")
	}
	if opened {
		t.Error("window must not open when the server never became ready")
	}
	if !stopped {
		t.Error("the just-started server must be stopped on failure")
	}
}

func TestLaunchStartServerFailurePropagates(t *testing.T) {
	l := Launcher{
		HealthCheck: func() bool { return false },
		StartServer: func() (func(), error) { return nil, errors.New("port busy") },
		WaitReady:   func() error { return nil },
		OpenWindow:  func() error { return nil },
	}
	if err := l.Launch(); err == nil {
		t.Fatal("StartServer failure must propagate")
	}
}

func TestProbeAgainstRealServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if !HealthCheckNow(srv.URL) {
		t.Error("HealthCheckNow must be true against a live /healthz")
	}
	if err := WaitReady(srv.URL, 3, time.Millisecond); err != nil {
		t.Errorf("WaitReady against a live server: %v", err)
	}
}

func TestProbeAgainstNothing(t *testing.T) {
	// Port 1 on loopback: nothing listens there in practice.
	if HealthCheckNow("http://127.0.0.1:1") {
		t.Error("HealthCheckNow must be false when nothing answers")
	}
	if err := WaitReady("http://127.0.0.1:1", 2, time.Millisecond); err == nil {
		t.Error("WaitReady must fail loudly after exhausting attempts")
	}
}
