package core

import (
	"fmt"
	"strings"
)

// ProcessSpec is one concrete command to execute for a project: the
// executable, its arguments, working folder and log file. Built from
// a CLIProcess definition by BuildProcessSpec — the resolution rule
// is the heart of F5: artisan & .php commands run through the
// PROJECT's php.exe, never a global PHP.
type ProcessSpec struct {
	Project string
	Name    string
	Exe     string
	Args    []string
	Dir     string
	LogPath string
}

// ProcessStarter starts one process and hands back its handle (port;
// the platform implementation wraps os/exec and owns the log file).
type ProcessStarter interface {
	Start(spec ProcessSpec) (ProcessHandle, error)
}

// ProcessHandle is a running process.
type ProcessHandle interface {
	// PID returns the OS process id (0 when unknown).
	PID() int
	// Wait blocks until the process exits and returns its exit code
	// (-1 when it died by signal/kill or the wait itself failed).
	Wait() int
	// Kill terminates the process.
	Kill() error
}

// OneShotRunner runs a command to completion capturing its combined
// output (port; used by the scheduler tick and the run-once API).
type OneShotRunner interface {
	Run(spec ProcessSpec) (output string, exitCode int, err error)
}

// SplitCommand splits a stored command string into fields, honoring
// double quotes ("arg with spaces"). Backslash escapes are not
// interpreted (Windows paths in arguments stay intact).
func SplitCommand(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote := false
	has := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			has = true
		case (r == ' ' || r == '\t') && !inQuote:
			if has || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("command has an unterminated quote: %q", s)
	}
	if has || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return out, nil
}

// BuildProcessSpec resolves a process definition (or an ad-hoc
// command) into an executable spec for one project.
func BuildProcessSpec(reg Registry, p Project, name, command, logDir string) (ProcessSpec, error) {
	fields, err := SplitCommand(command)
	if err != nil {
		return ProcessSpec{}, err
	}
	spec := ProcessSpec{
		Project: p.Name,
		Name:    name,
		Dir:     p.FSPath,
		LogPath: joinPath(joinPath(logDir, p.Name), name+".log"),
	}
	head := fields[0]
	if head == "artisan" || strings.HasSuffix(strings.ToLower(head), ".php") {
		v, ok := reg.FindPHP(p.PHPVersion)
		if !ok {
			return ProcessSpec{}, fmt.Errorf("project %q has no registered php version %q to run %q with", p.Name, p.PHPVersion, command)
		}
		spec.Exe = joinPath(v.Path, "php.exe")
		spec.Args = fields // php.exe artisan ... / php.exe script.php ...
	} else {
		spec.Exe = head
		spec.Args = fields[1:]
	}
	// Best effort: every (re)start retires this process's dated logs
	// older than the retention window. A prune failure never fails
	// the start (see PruneProcessLogs).
	PruneProcessLogs(logDir, p.Name, name, LogRetentionDays)
	return spec, nil
}

// ScheduleRunSpec builds the one-shot spec for a project's
// `artisan schedule:run` (the Scheduler's per-minute job).
func ScheduleRunSpec(reg Registry, p Project, logDir string) (ProcessSpec, error) {
	return BuildProcessSpec(reg, p, "scheduler", "artisan schedule:run", logDir)
}
