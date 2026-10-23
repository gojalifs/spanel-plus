//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"golang.org/x/sys/windows/svc"
)

// RunPanel runs the panel either as a Windows Service (when the SCM
// started this process) or as an interactive console program
// (Ctrl+C stops it cleanly). This is the M1 service criterion:
// one binary, native Windows Service, no NSSM (Plan D6).
func RunPanel(serviceName string, run func(context.Context) error) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("detect windows service mode: %w", err)
	}
	if !isService {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return run(ctx)
	}
	return svc.Run(serviceName, &panelService{run: run})
}

// panelService adapts the panel's run function to the SCM protocol.
type panelService struct {
	run func(context.Context) error
}

// Execute implements svc.Handler. The panel runs in a goroutine on a
// cancellable context; Stop/Shutdown cancels it and waits for a clean
// exit before reporting Stopped to the SCM.
func (s *panelService) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()

	changes <- svc.Status{State: svc.Running, Accepts: accepts}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case err := <-done:
			// The panel exited on its own (e.g. port bind failed).
			// Report a service-specific failure so the SCM marks the
			// service as failed instead of cleanly stopped — fail loud.
			cancel()
			if err != nil {
				return true, 1
			}
			return false, 0
		}
	}
}
