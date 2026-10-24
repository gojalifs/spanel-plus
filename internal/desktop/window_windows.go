//go:build windows

package desktop

import (
	"errors"
	"runtime"

	"github.com/jchv/go-webview2"
)

// OpenWindow shows the panel UI in a WebView2 desktop window and
// blocks until the user closes it. Pure Go (no CGO): WebView2 is
// driven through COM via go-webview2, and the runtime ships with
// Microsoft Edge on Windows 10/11.
//
// The window must live on the main OS thread; Launch is called from
// main without any goroutine hop, and we lock the thread to be sure.
func OpenWindow(url string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := webview2.New(false)
	if w == nil {
		return errors.New("WebView2 Runtime tidak ditemukan — pasang Microsoft Edge WebView2 Runtime (biasanya sudah termasuk di Windows 10/11 lewat Edge), atau jalankan 'panel serve' lalu buka UI dari browser")
	}
	defer w.Destroy()
	w.SetTitle("SPanel+")
	w.SetSize(1150, 780, webview2.HintNone)
	w.Navigate(url)
	w.Run()
	return nil
}
