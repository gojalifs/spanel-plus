//go:build !windows

package platform

// DetachConsole is a no-op off Windows (no console window to detach).
func DetachConsole() {}
