package core

import (
	"context"
	"log/slog"
	"time"
)

// Scheduler is the panel's minute ticker (Plan F5): every interval
// it runs `php artisan schedule:run` — with each project's own
// php.exe — for every enabled project that registered a schedule:run
// hook with autostart. This replaces the Windows Task Scheduler
// entry Laravel deployments usually need.
type Scheduler struct {
	Supervisor *Supervisor
	Registry   *RegistryService
	Interval   time.Duration // default 1 minute
	// NewTicker is injectable for tests; nil uses time.NewTicker.
	NewTicker func(d time.Duration) (ticks <-chan time.Time, stop func())
	Log       *slog.Logger
}

// Run ticks until ctx is cancelled.
func (sc *Scheduler) Run(ctx context.Context) {
	interval := sc.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	newTicker := sc.NewTicker
	if newTicker == nil {
		newTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	ticks, stop := newTicker(interval)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			sc.Tick(ctx)
		}
	}
}

// Tick runs one scheduling round for all due projects. Projects run
// sequentially — schedule:run is expected to be quick (it dispatches
// due jobs and returns); a hung project delays the round, which the
// log makes visible.
func (sc *Scheduler) Tick(ctx context.Context) {
	reg, err := sc.Registry.Load()
	if err != nil {
		if sc.Log != nil {
			sc.Log.Error("scheduler cannot load registry", "err", err)
		}
		return
	}
	for _, p := range reg.Projects {
		if !p.Enabled {
			continue
		}
		for _, def := range p.CLIProcesses {
			if def.Kind != ProcessKindScheduleRun || !def.Autostart {
				continue
			}
			out, code, err := sc.Supervisor.RunScheduleOnce(ctx, p.Name)
			if sc.Log != nil {
				if err != nil {
					sc.Log.Error("schedule:run failed to start", "project", p.Name, "err", err)
				} else if code != 0 {
					sc.Log.Warn("schedule:run exited non-zero", "project", p.Name, "exit", code, "output_tail", tailString(out, 300))
				}
			}
		}
	}
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
