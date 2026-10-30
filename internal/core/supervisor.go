package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ProcessState is the supervisor's view of one managed process.
type ProcessState string

const (
	StateRunning   ProcessState = "running"
	StateBackoff   ProcessState = "backoff"   // crashed, waiting to restart
	StateStopped   ProcessState = "stopped"   // stopped on request
	StateExited    ProcessState = "exited"    // ended, policy says no restart
	StateFailed    ProcessState = "failed"    // gave up after MaxAttempts
	StateIdle      ProcessState = "idle"      // known definition, never started
	StateScheduled ProcessState = "scheduled" // scheduler hook (not a process)
)

// ProcessStatus is one process definition's runtime status.
type ProcessStatus struct {
	Project   string       `json:"project"`
	Name      string       `json:"name"`
	Kind      string       `json:"kind"`
	Command   string       `json:"command"`
	Autostart bool         `json:"autostart"`
	State     ProcessState `json:"state"`
	PID       int          `json:"pid"`
	Restarts  int          `json:"restarts"`
	LastExit  int          `json:"last_exit"`
	StartedAt time.Time    `json:"started_at"`
	LastRunAt time.Time    `json:"last_run_at"` // scheduler: last schedule:run tick
}

// SupervisorConfig tunes the restart behavior (Plan F5).
type SupervisorConfig struct {
	BackoffBase time.Duration // first restart delay (default 1s)
	BackoffMax  time.Duration // delay cap (default 30s)
	MaxAttempts int           // consecutive unstable exits before giving up (default 5)
	StableAfter time.Duration // a run at least this long resets the attempt counter (default 60s)
}

func (c SupervisorConfig) withDefaults() SupervisorConfig {
	if c.BackoffBase <= 0 {
		c.BackoffBase = time.Second
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 30 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.StableAfter <= 0 {
		c.StableAfter = 60 * time.Second
	}
	return c
}

// Supervisor keeps per-project long-running CLI processes alive:
// autostart, restart with exponential backoff, a hard attempt cap,
// and a status snapshot for the dashboard. All process execution
// sits behind ProcessStarter; all waiting behind the injected Sleep,
// so the whole lifecycle is unit-testable (Plan 10.1).
type Supervisor struct {
	Registry *RegistryService
	Starter  ProcessStarter
	OneShot  OneShotRunner
	Clock    Clock
	Sleep    func(ctx context.Context, d time.Duration) bool // false = interrupted
	Log      *slog.Logger
	LogDir   string // panel data projects dir; logs land in <LogDir>/<project>/<name>.log
	Cfg      SupervisorConfig

	mu    sync.Mutex
	root  context.Context
	procs map[string]*managedProc
	sched map[string]time.Time // project -> last schedule:run time
}

type managedProc struct {
	project string
	def     CLIProcess
	cancel  context.CancelFunc
	done    chan struct{}
	handle  ProcessHandle
	status  ProcessStatus
}

// Start attaches the supervisor to the panel's run context and
// autostarts every enabled project's autostart processes.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	s.root = ctx
	if s.procs == nil {
		s.procs = map[string]*managedProc{}
	}
	if s.sched == nil {
		s.sched = map[string]time.Time{}
	}
	s.mu.Unlock()

	reg, err := s.Registry.Load()
	if err != nil {
		return err
	}
	for _, p := range reg.Projects {
		if !p.Enabled {
			continue
		}
		for _, def := range p.CLIProcesses {
			if def.Autostart && def.Kind == ProcessKindLongRunning {
				if err := s.StartProcess(p.Name, def.Name); err != nil && s.Log != nil {
					s.Log.Error("supervisor autostart failed", "project", p.Name, "process", def.Name, "err", err)
				} else if err == nil && s.Log != nil {
					s.Log.Info("supervisor autostart ok", "project", p.Name, "process", def.Name)
				}
			}
		}
	}
	return nil
}

// StartProcess starts one long-running process definition.
func (s *Supervisor) StartProcess(projectName, procName string) error {
	reg, err := s.Registry.Load()
	if err != nil {
		return err
	}
	p, ok := reg.FindProject(projectName)
	if !ok {
		return fmt.Errorf("project %q not found: %w", projectName, ErrProjectNotFound)
	}
	def, ok := findProcessDef(p, procName)
	if !ok {
		return fmt.Errorf("project %q has no cli process %q: %w", projectName, procName, ErrProcessNotFound)
	}
	if def.Kind == ProcessKindScheduleRun {
		return fmt.Errorf("process %q of project %q is a scheduler hook, not a long-running process", procName, projectName)
	}
	key := procKey(projectName, procName)
	s.mu.Lock()
	if s.root == nil {
		s.mu.Unlock()
		return fmt.Errorf("supervisor is not started")
	}
	if mp, exists := s.procs[key]; exists {
		st := mp.status.State
		s.mu.Unlock()
		if st == StateRunning || st == StateBackoff {
			return fmt.Errorf("process %q of project %q is already %s", procName, projectName, st)
		}
	}
	ctx, cancel := context.WithCancel(s.root)
	mp := &managedProc{
		project: projectName,
		def:     def,
		cancel:  cancel,
		done:    make(chan struct{}),
		// StateRunning is only set once the process actually started
		// (setHandle) — before that the process is starting/idle.
		status: ProcessStatus{Project: projectName, Name: procName, Kind: def.Kind, Command: def.Command, Autostart: def.Autostart, State: StateIdle, LastExit: -1},
	}
	s.procs[key] = mp
	s.mu.Unlock()

	go s.supervise(ctx, reg, p, mp)
	return nil
}

// supervise is the per-process lifecycle loop.
func (s *Supervisor) supervise(ctx context.Context, reg Registry, p Project, mp *managedProc) {
	defer close(mp.done)
	cfg := s.Cfg.withDefaults()
	attempts := 0
	for {
		spec, err := BuildProcessSpec(reg, p, mp.def.Name, mp.def.Command, s.LogDir)
		var code int
		var startedAt time.Time
		if err != nil {
			code = -1
			if s.Log != nil {
				s.Log.Error("supervisor cannot build command", "project", mp.project, "process", mp.def.Name, "err", err)
			}
		} else {
			handle, serr := s.Starter.Start(spec)
			if serr != nil {
				code = -1
				if s.Log != nil {
					s.Log.Error("supervisor start failed", "project", mp.project, "process", mp.def.Name, "err", serr)
				}
			} else {
				startedAt = s.now()
				s.setHandle(mp, handle, startedAt)
				code = handle.Wait()
				s.clearHandle(mp, code)
			}
		}

		if ctx.Err() != nil {
			s.setState(mp, StateStopped)
			return
		}
		policy := mp.def.Restart
		if policy == "" {
			policy = "on-failure"
		}
		if policy == "never" || (policy == "on-failure" && code == 0) {
			s.setState(mp, StateExited)
			return
		}
		if startedAt.IsZero() || s.now().Sub(startedAt) < cfg.StableAfter {
			attempts++
		} else {
			attempts = 0
		}
		s.bumpRestarts(mp)
		if attempts > cfg.MaxAttempts {
			s.setState(mp, StateFailed)
			if s.Log != nil {
				s.Log.Error("supervisor gave up", "project", mp.project, "process", mp.def.Name, "attempts", attempts, "last_exit", code)
			}
			return
		}
		delay := backoff(cfg, attempts)
		s.setState(mp, StateBackoff)
		if !s.sleep(ctx, delay) {
			s.setState(mp, StateStopped)
			return
		}
		s.setState(mp, StateRunning)
	}
}

// StopProcess stops one process and waits for its loop to end.
func (s *Supervisor) StopProcess(projectName, procName string) error {
	key := procKey(projectName, procName)
	s.mu.Lock()
	mp, ok := s.procs[key]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("process %q of project %q is not running: %w", procName, projectName, ErrProcessNotFound)
	}
	mp.cancel()
	// The handle appears asynchronously (the supervise loop may not
	// have started the process yet), so keep offering the kill until
	// the loop is done — a single early read could miss it and leave
	// the loop blocked in Wait forever.
	for {
		s.mu.Lock()
		h := mp.handle
		s.mu.Unlock()
		if h != nil {
			_ = h.Kill()
		}
		select {
		case <-mp.done:
			return nil
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// RestartProcess stops then starts one process.
func (s *Supervisor) RestartProcess(projectName, procName string) error {
	if err := s.StopProcess(projectName, procName); err != nil {
		return err
	}
	return s.StartProcess(projectName, procName)
}

// StopAll stops every managed process (panel shutdown).
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	keys := make([][2]string, 0, len(s.procs))
	for _, mp := range s.procs {
		keys = append(keys, [2]string{mp.project, mp.def.Name})
	}
	s.mu.Unlock()
	for _, k := range keys {
		_ = s.StopProcess(k[0], k[1])
	}
}

// Statuses merges registry definitions with live supervisor state —
// the dashboard's complete process list.
func (s *Supervisor) Statuses() ([]ProcessStatus, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return nil, err
	}
	var out []ProcessStatus
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range reg.Projects {
		for _, def := range p.CLIProcesses {
			st := ProcessStatus{
				Project: p.Name, Name: def.Name, Kind: def.Kind, Command: def.Command,
				Autostart: def.Autostart, State: StateIdle, LastExit: -1,
			}
			if def.Kind == ProcessKindScheduleRun {
				st.State = StateScheduled
				st.LastRunAt = s.sched[p.Name]
			}
			if mp, ok := s.procs[procKey(p.Name, def.Name)]; ok {
				live := mp.status
				live.Command = def.Command
				live.Autostart = def.Autostart
				out = append(out, live)
				continue
			}
			out = append(out, st)
		}
	}
	return out, nil
}

// RunScheduleOnce executes `artisan schedule:run` for one project
// right now (the Scheduler calls this on every tick; the API exposes
// it for manual runs). Output is appended to the scheduler log by
// the one-shot runner and returned.
func (s *Supervisor) RunScheduleOnce(ctx context.Context, projectName string) (string, int, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return "", -1, err
	}
	p, ok := reg.FindProject(projectName)
	if !ok {
		return "", -1, fmt.Errorf("project %q not found: %w", projectName, ErrProjectNotFound)
	}
	spec, err := ScheduleRunSpec(reg, p, s.LogDir)
	if err != nil {
		return "", -1, err
	}
	out, code, err := s.OneShot.Run(spec)
	s.mu.Lock()
	if s.sched == nil {
		s.sched = map[string]time.Time{}
	}
	s.sched[projectName] = s.now()
	s.mu.Unlock()
	return out, code, err
}

// RunOnce runs an ad-hoc command for a project with the project's
// php.exe (artisan/*.php rule) and returns its combined output.
func (s *Supervisor) RunOnce(ctx context.Context, projectName, command string) (string, int, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return "", -1, err
	}
	p, ok := reg.FindProject(projectName)
	if !ok {
		return "", -1, fmt.Errorf("project %q not found: %w", projectName, ErrProjectNotFound)
	}
	spec, err := BuildProcessSpec(reg, p, "run-once", command, s.LogDir)
	if err != nil {
		return "", -1, err
	}
	_ = ctx
	return s.OneShot.Run(spec)
}

func findProcessDef(p Project, name string) (CLIProcess, bool) {
	for _, d := range p.CLIProcesses {
		if d.Name == name {
			return d, true
		}
	}
	return CLIProcess{}, false
}

func procKey(project, name string) string { return project + "/" + name }

func backoff(cfg SupervisorConfig, attempts int) time.Duration {
	d := cfg.BackoffBase
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= cfg.BackoffMax {
			return cfg.BackoffMax
		}
	}
	if d > cfg.BackoffMax {
		return cfg.BackoffMax
	}
	return d
}

func (s *Supervisor) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return time.Now()
}

func (s *Supervisor) sleep(ctx context.Context, d time.Duration) bool {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *Supervisor) setHandle(mp *managedProc, h ProcessHandle, startedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mp.handle = h
	mp.status.State = StateRunning
	mp.status.PID = h.PID()
	mp.status.StartedAt = startedAt
}

func (s *Supervisor) clearHandle(mp *managedProc, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mp.handle = nil
	mp.status.PID = 0
	mp.status.LastExit = code
}

func (s *Supervisor) setState(mp *managedProc, st ProcessState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mp.status.State = st
}

func (s *Supervisor) bumpRestarts(mp *managedProc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mp.status.Restarts++
}
