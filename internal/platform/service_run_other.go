//go:build !windows

package platform

import (
	"context"
	"os"
	"os/signal"
)

// RunPanel executes run in the foreground with a context that is
// cancelled on Ctrl+C. Windows builds use service_run_windows.go,
// which additionally detects and serves the Service Control Manager.
func RunPanel(_ string, run func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return run(ctx)
}
