//go:build !windows

package platform

// IsWindowsService is always false outside Windows: there is no SCM.
func IsWindowsService() bool { return false }

// SyncServiceDisplayName is a no-op outside Windows: there is no SCM
// display name to keep in sync.
func SyncServiceDisplayName() error { return nil }
