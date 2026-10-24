package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fake process starter/handles ---

type fakeHandle struct {
	code  int
	block bool

	killCh chan struct{}
	once   sync.Once
}

func (h *fakeHandle) PID() int { return 4321 }
func (h *fakeHandle) Wait() int {
	if h.block {
		<-h.killCh
		return -1
	}
	return h.code
}
func (h *fakeHandle) Kill() error {
	h.once.Do(func() { close(h.killCh) })
	return nil
}

type fakeStarter struct {
	mu      sync.Mutex
	starts  []ProcessSpec
	handles []*fakeHandle
	defCode int
	block   bool
}

func (f *fakeStarter) Start(spec ProcessSpec) (ProcessHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, spec)
	h := &fakeHandle{code: f.defCode, block: f.block, killCh: make(chan struct{})}
	f.handles = append(f.handles, h)
	return h, nil
}
func (f *fakeStarter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

type fakeOneShot struct {
	mu    sync.Mutex
	specs []ProcessSpec
}

func (f *fakeOneShot) Run(spec ProcessSpec) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, spec)
	return "ok-output", 0, nil
}

func supervisorRig(t *testing.T, starter *fakeStarter, oneShot *fakeOneShot, cfg SupervisorConfig) *Supervisor {
	t.Helper()
	reg := baseRegistry("8.2")
	reg.Projects[0].CLIProcesses = []CLIProcess{
		{Name: "queue", Command: "artisan queue:work", Autostart: true, Restart: "on-failure"},
		{Name: "scheduler", Kind: ProcessKindScheduleRun, Autostart: true},
	}
	store := &memStore{reg: reg, hasReg: true}
	regSvc := &RegistryService{Store: store, Files: fullEnvForManager(), Clock: &tickClock{t: time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC)}}
	return &Supervisor{
		Registry: regSvc,
		Starter:  starter,
		OneShot:  oneShot,
		Clock:    &tickClock{t: time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC)},
		Sleep:    func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil },
		LogDir:   `C:\Panel\data\projects`,
		Cfg:      cfg,
	}
}

func waitStatus(t *testing.T, sup *Supervisor, want ProcessState) ProcessStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sts, err := sup.Statuses()
		if err != nil {
			t.Fatalf("Statuses: %v", err)
		}
		for _, st := range sts {
			if st.Name == "queue" && st.State == want {
				return st
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	sts, _ := sup.Statuses()
	t.Fatalf("status never became %q; statuses=%+v", want, sts)
	return ProcessStatus{}
}

func TestSupervisorCrashLoopGivesUp(t *testing.T) {
	starter := &fakeStarter{defCode: 1}
	sup := supervisorRig(t, starter, &fakeOneShot{}, SupervisorConfig{MaxAttempts: 3, BackoffBase: time.Millisecond, StableAfter: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitStatus(t, sup, StateFailed)
	if starter.count() != 4 { // initial + 3 restarts; 4th failure exceeds MaxAttempts=3
		t.Errorf("starts = %d, want 4", starter.count())
	}
	if st.Restarts < 3 || st.LastExit != 1 {
		t.Errorf("status after give-up = %+v", st)
	}
	spec := starter.starts[0]
	if !strings.HasSuffix(spec.Exe, "php.exe") || len(spec.Args) == 0 || spec.Args[0] != "artisan" {
		t.Errorf("queue must run through the project's php.exe, spec=%+v", spec)
	}
}

func TestSupervisorCleanExitAndStop(t *testing.T) {
	// Clean exit with on-failure policy: exited, no restart.
	starter := &fakeStarter{defCode: 0}
	sup := supervisorRig(t, starter, &fakeOneShot{}, SupervisorConfig{MaxAttempts: 3})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, sup, StateExited)
	if starter.count() != 1 {
		t.Errorf("clean exit must not restart, starts=%d", starter.count())
	}

	// Blocking process: stop kills it and prevents restart.
	starter2 := &fakeStarter{block: true}
	sup2 := supervisorRig(t, starter2, &fakeOneShot{}, SupervisorConfig{MaxAttempts: 3})
	if err := sup2.Start(ctx); err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	st := waitStatus(t, sup2, StateRunning)
	if st.PID != 4321 {
		t.Errorf("running status must carry the pid, got %+v", st)
	}
	if err := sup2.StopProcess("web-app", "queue"); err != nil {
		t.Fatalf("StopProcess: %v", err)
	}
	waitStatus(t, sup2, StateStopped)
	if starter2.count() != 1 {
		t.Errorf("stopped process must not restart, starts=%d", starter2.count())
	}
	if err := sup2.StopProcess("web-app", "hantu"); err == nil {
		t.Error("stopping an unknown process must error")
	}
	if err := sup2.StartProcess("web-app", "scheduler"); err == nil {
		t.Error("starting a scheduler hook as a process must error")
	}
}

func TestSchedulerTickAndRunOnce(t *testing.T) {
	oneShot := &fakeOneShot{}
	sup := supervisorRig(t, &fakeStarter{}, oneShot, SupervisorConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sup.StopAll()

	sc := &Scheduler{Supervisor: sup, Registry: sup.Registry}
	sc.Tick(context.Background())
	if len(oneShot.specs) != 1 {
		t.Fatalf("scheduler tick must run schedule:run exactly once, got %d", len(oneShot.specs))
	}
	spec := oneShot.specs[0]
	if !strings.HasSuffix(spec.Exe, "php.exe") || len(spec.Args) != 2 || spec.Args[1] != "schedule:run" {
		t.Errorf("schedule spec wrong: %+v", spec)
	}
	sts, _ := sup.Statuses()
	for _, st := range sts {
		if st.Name == "scheduler" {
			if st.State != StateScheduled || st.LastRunAt.IsZero() {
				t.Errorf("scheduler status = %+v, want scheduled with last run stamped", st)
			}
		}
	}

	out, code, err := sup.RunOnce(context.Background(), "web-app", "artisan migrate:status")
	if err != nil || code != 0 || out != "ok-output" {
		t.Errorf("RunOnce = %q, %d, %v", out, code, err)
	}
}

func TestSplitAndBuildSpec(t *testing.T) {
	fields, err := SplitCommand(`artisan queue:work --queue="high prio" --tries=3`)
	if err != nil || len(fields) != 4 || fields[2] != "--queue=high prio" {
		t.Errorf("SplitCommand = %v, %v", fields, err)
	}
	if _, err := SplitCommand(`artisan "rusak`); err == nil {
		t.Error("unterminated quote must error")
	}
	reg := baseRegistry("8.2")
	p := reg.Projects[0]
	spec, err := BuildProcessSpec(reg, p, "worker", "worker.php", `C:\logs`)
	if err != nil || !strings.HasSuffix(spec.Exe, "php.exe") || spec.Args[0] != "worker.php" {
		t.Errorf(".php command must go through php.exe: %+v, %v", spec, err)
	}
	spec, err = BuildProcessSpec(reg, p, "alat", "mytool.exe --flag", `C:\logs`)
	if err != nil || spec.Exe != "mytool.exe" || len(spec.Args) != 1 {
		t.Errorf("foreign exe must run directly: %+v, %v", spec, err)
	}
}

func TestTailLog(t *testing.T) {
	fs := newMemFS()
	_ = fs.WriteFile(`C:\logs\app.log`, []byte("satu\ndua\ntiga\nempat\n"))
	got, err := TailLog(fs, `C:\logs\app.log`, 2, 0)
	if err != nil || got != "tiga\nempat" {
		t.Errorf("TailLog = %q, %v", got, err)
	}
	got, err = TailLog(fs, `C:\logs\belum-ada.log`, 10, 0)
	if err != nil || got != "" {
		t.Errorf("missing log must be empty, got %q, %v", got, err)
	}
}
