//go:build windows

package platform

import "golang.org/x/sys/windows"

var procFreeConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole")

// DetachConsole releases the console window a double-clicked
// console-subsystem binary gets, so desktop mode shows only the
// WebView2 window. Best effort: failure leaves the console attached,
// which is cosmetic, not functional. (x/sys/windows has no FreeConsole
// wrapper, so the DLL proc is called directly.)
func DetachConsole() {
	_, _, _ = procFreeConsole.Call()
}
