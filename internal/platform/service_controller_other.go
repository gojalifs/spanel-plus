//go:build !windows

package platform

import (
	"errors"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// ErrServiceUnsupported is returned by every service operation off
// Windows: the panel's service management is Windows-only by design
// (Plan D6), and pretending otherwise would fail silently later.
var ErrServiceUnsupported = errors.New("service management is only available on Windows")

type unsupportedServiceController struct{}

func (unsupportedServiceController) Install() error   { return ErrServiceUnsupported }
func (unsupportedServiceController) Uninstall() error { return ErrServiceUnsupported }
func (unsupportedServiceController) Start() error     { return ErrServiceUnsupported }
func (unsupportedServiceController) Stop() error      { return ErrServiceUnsupported }
func (unsupportedServiceController) Status() (string, error) {
	return "", ErrServiceUnsupported
}

// NewServiceController returns the OS service manager implementation.
func NewServiceController() core.ServiceController {
	return unsupportedServiceController{}
}
