//go:build !windows

package desktop

import "errors"

// OpenWindow is Windows-only: off Windows, fail loudly and point at
// the supported alternative instead of silently doing nothing.
func OpenWindow(string) error {
	return errors.New("mode desktop hanya tersedia di Windows — di platform ini jalankan 'panel serve' dan buka UI dari browser")
}
