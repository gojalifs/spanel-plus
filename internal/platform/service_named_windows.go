//go:build windows

package platform

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// scmNamedController controls any Windows service by name through
// the SCM — the managed Apache for the apply pipeline.
type scmNamedController struct{}

// NewNamedServiceController returns the SCM by-name controller.
func NewNamedServiceController() core.ApacheServiceControl {
	return scmNamedController{}
}

func (scmNamedController) Restart(name string) error {
	m, s, err := openServiceByName(name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("query service %s: %w", name, err)
	}
	if st.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("stop service %s: %w", name, err)
		}
		if err := waitState(s, svc.Stopped, 20*time.Second); err != nil {
			return err
		}
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %s: %w", name, err)
	}
	return waitState(s, svc.Running, 20*time.Second)
}

func (scmNamedController) Status(name string) (string, error) {
	m, s, err := openServiceByName(name)
	if err != nil {
		return "", err
	}
	defer m.Disconnect()
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return "", fmt.Errorf("query service %s: %w", name, err)
	}
	return stateName(st.State), nil
}

func openServiceByName(name string) (*mgr.Mgr, *mgr.Service, error) {
	m, err := mgr.ConnectRemote("")
	if err != nil {
		return nil, nil, fmt.Errorf("connect to service manager (run as Administrator?): %w", err)
	}
	s, err := m.OpenService(name)
	if err != nil {
		m.Disconnect()
		return nil, nil, fmt.Errorf("open service %s (does it exist?): %w", name, err)
	}
	return m, s, nil
}
