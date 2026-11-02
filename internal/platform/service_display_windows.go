//go:build windows

package platform

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"github.com/gojalifs/spanel-plus/internal/buildinfo"
)

// IsWindowsService reports whether this process was started by the
// Service Control Manager (session-0 parent "services"). Console and
// desktop runs return false, so only the real service ever relabels
// itself — a newer binary run by hand must not rename the service
// that still runs the older binary.
func IsWindowsService() bool {
	is, err := svc.IsWindowsService()
	return err == nil && is
}

// SyncServiceDisplayName makes the SCM display name match the running
// binary's version (buildinfo.VersionedServiceDisplayName). It reads
// the current name first and only writes when it differs, so a
// restart that changes nothing touches nothing. Fail-soft by contract:
// callers log the error and continue — a cosmetic label must never
// fail a panel start.
func SyncServiceDisplayName() error {
	desired := buildinfo.VersionedServiceDisplayName()
	mgr, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("open service manager: %w", err)
	}
	defer windows.CloseServiceHandle(mgr)

	svcHandle, err := windows.OpenService(mgr, windows.StringToUTF16Ptr(buildinfo.ServiceName), windows.SERVICE_QUERY_CONFIG|windows.SERVICE_CHANGE_CONFIG)
	if err != nil {
		return fmt.Errorf("open service %s: %w", buildinfo.ServiceName, err)
	}
	defer windows.CloseServiceHandle(svcHandle)

	// Query the current display name: first call with a zero buffer
	// reports the required size via ERROR_INSUFFICIENT_BUFFER.
	var needed uint32
	if err := windows.QueryServiceConfig(svcHandle, nil, 0, &needed); err != windows.ERROR_INSUFFICIENT_BUFFER {
		return fmt.Errorf("query service config size: %w", err)
	}
	if needed == 0 {
		return fmt.Errorf("query service config size: SCM returned zero size")
	}
	buf := make([]byte, needed)
	cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0]))
	if err := windows.QueryServiceConfig(svcHandle, cfg, needed, &needed); err != nil {
		return fmt.Errorf("query service config: %w", err)
	}
	if windows.UTF16PtrToString(cfg.DisplayName) == desired {
		return nil // already correct; touch nothing
	}
	if err := windows.ChangeServiceConfig(svcHandle, windows.SERVICE_NO_CHANGE, windows.SERVICE_NO_CHANGE, windows.SERVICE_NO_CHANGE, nil, nil, nil, nil, nil, nil, windows.StringToUTF16Ptr(desired)); err != nil {
		return fmt.Errorf("change display name to %q: %w", desired, err)
	}
	return nil
}
