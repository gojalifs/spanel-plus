//go:build windows

package platform

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/gojalifs/spanel-plus/internal/buildinfo"
	"github.com/gojalifs/spanel-plus/internal/core"
)

// scmServiceController implements core.ServiceController against the
// Windows Service Control Manager. Every operation needs an elevated
// (Administrator) shell; SCM errors are returned verbatim — fail loud.
type scmServiceController struct{}

// NewServiceController returns the Windows SCM implementation.
func NewServiceController() core.ServiceController {
	return scmServiceController{}
}

func (scmServiceController) Install() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}
	m, err := mgr.ConnectRemote("")
	if err != nil {
		return fmt.Errorf("connect to service manager (run as Administrator?): %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(buildinfo.ServiceName); err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists — uninstall it first", buildinfo.ServiceName)
	}
	s, err := m.CreateService(buildinfo.ServiceName, exe, mgr.Config{
		DisplayName: buildinfo.ServiceDisplayName,
		Description: "SPanel+ admin panel: one Apache, multi-PHP per project path (internal use).",
		StartType:   mgr.StartAutomatic, // start at boot (M1 criterion)
	}, "serve")
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()
	return nil
}

func (scmServiceController) Uninstall() error {
	m, s, err := openPanelService()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	// Best effort stop: deleting a running service only marks it for
	// deletion; stopping first makes uninstall immediate and clean.
	_, _ = s.Control(svc.Stop)
	if err := waitState(s, svc.Stopped, 10*time.Second); err != nil {
		return err
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	return nil
}

func (scmServiceController) Start() error {
	m, s, err := openPanelService()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return waitState(s, svc.Running, 15*time.Second)
}

func (scmServiceController) Stop() error {
	m, s, err := openPanelService()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop service: %w", err)
	}
	return waitState(s, svc.Stopped, 15*time.Second)
}

func (scmServiceController) Status() (string, error) {
	m, s, err := openPanelService()
	if err != nil {
		return "", err
	}
	defer m.Disconnect()
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return "", fmt.Errorf("query service: %w", err)
	}
	return stateName(st.State), nil
}

func openPanelService() (*mgr.Mgr, *mgr.Service, error) {
	m, err := mgr.ConnectRemote("")
	if err != nil {
		return nil, nil, fmt.Errorf("connect to service manager (run as Administrator?): %w", err)
	}
	s, err := m.OpenService(buildinfo.ServiceName)
	if err != nil {
		m.Disconnect()
		return nil, nil, fmt.Errorf("open service %s (is it installed?): %w", buildinfo.ServiceName, err)
	}
	return m, s, nil
}

// waitState polls until the service reaches want or the timeout hits.
func waitState(s *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("query service state: %w", err)
		}
		if st.State == want {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("service did not reach state %s within %s", stateName(want), timeout)
}

func stateName(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start-pending"
	case svc.StopPending:
		return "stop-pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue-pending"
	case svc.PausePending:
		return "pause-pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}
