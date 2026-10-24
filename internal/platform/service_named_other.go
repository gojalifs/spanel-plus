//go:build !windows

package platform

import (
	"github.com/gojalifs/spanel-plus/internal/core"
)

// namedServiceControllerStub fails loudly off Windows.
type namedServiceControllerStub struct{}

// NewNamedServiceController returns the by-name service controller
// used for the managed Apache service.
func NewNamedServiceController() core.ApacheServiceControl {
	return namedServiceControllerStub{}
}

func (namedServiceControllerStub) Restart(string) error {
	return ErrServiceUnsupported
}

func (namedServiceControllerStub) Status(string) (string, error) {
	return "", ErrServiceUnsupported
}
