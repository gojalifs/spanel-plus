package core

import "strings"

// ProcessLogPath is where a process definition's log lives.
func ProcessLogPath(logDir, project, name string) string {
	return joinPath(joinPath(logDir, project), name+".log")
}

// TailLog returns the last maxLines lines of a log file (reading at
// most the last maxBytes so a runaway log cannot exhaust memory).
// A missing log is an empty result, not an error — the process may
// simply not have run yet.
func TailLog(files FileStore, path string, maxLines, maxBytes int) (string, error) {
	raw, err := files.ReadFile(path)
	if err != nil {
		return "", nil
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		raw = raw[len(raw)-maxBytes:]
		// Drop the partial first line of the window.
		if i := strings.IndexByte(string(raw), '\n'); i >= 0 {
			raw = raw[i+1:]
		}
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n"), nil
}
