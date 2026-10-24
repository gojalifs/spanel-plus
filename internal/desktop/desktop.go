// Package desktop opens the panel as a desktop application: a native
// window (WebView2 on Windows) pointed at the panel's own embedded UI.
//
// Keputusan max (2026-10-24): panel diakses sebagai aplikasi desktop
// lewat exe, bukan sebagai tab browser. Teknologi UI-nya tidak berubah
// (HTML ter-embed + API JSON yang sama); yang berubah hanya wadahnya —
// dari tab browser menjadi jendela desktop. Mode `serve` tetap ada
// untuk Windows Service dan pemakaian headless.
package desktop

import "fmt"

// Launcher orchestrates the desktop mode. All collaborators are
// injected functions so this decision logic is unit-testable without
// a real window or server (Plan 10.1):
//
//   - A panel server may already be running (e.g. the Windows
//     Service started at boot). Then the window simply points at it,
//     and closing the window must NOT stop that server.
//   - Otherwise the launcher starts its own in-process server and
//     stops it when the window closes.
type Launcher struct {
	// HealthCheck reports whether a panel server already answers.
	HealthCheck func() bool
	// StartServer starts the panel server in the background and
	// returns a function that stops it.
	StartServer func() (stop func(), err error)
	// WaitReady blocks until the just-started server answers.
	WaitReady func() error
	// OpenWindow shows the desktop window and blocks until it closes.
	OpenWindow func() error
}

// Launch runs the desktop mode until the window is closed.
func (l Launcher) Launch() error {
	if l.HealthCheck() {
		// Server milik proses lain (service) — jangan disentuh.
		return l.OpenWindow()
	}
	stop, err := l.StartServer()
	if err != nil {
		return fmt.Errorf("start panel server: %w", err)
	}
	defer stop()
	if err := l.WaitReady(); err != nil {
		return fmt.Errorf("panel server tidak siap: %w", err)
	}
	return l.OpenWindow()
}
