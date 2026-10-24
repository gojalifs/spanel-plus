package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// PHPProbeResult is what probing one PHP install folder yields: the
// detected version, build flavor and loaded modules (Plan F2
// validation). The probing itself (running php.exe) sits behind the
// PHPProber port; the output parsing lives here so it is unit-tested.
type PHPProbeResult struct {
	Version string   // "8.3" — major.minor as the registry keys versions
	Full    string   // "8.3.13" — the full build version
	NTS     bool     // non-thread-safe build (required by mod_fcgid)
	Arch    string   // "x64" | "x86" | "arm64" | ""
	Modules []string // php -m output, one module per entry
}

// PHPProber probes a folder holding php.exe.
type PHPProber interface {
	Probe(ctx context.Context, dir string) (PHPProbeResult, error)
}

var phpVersionOutputRe = regexp.MustCompile(`^PHP (\d+)\.(\d+)\.(\d+)`)

// ParsePHPVersionOutput parses the first lines of `php.exe -v`.
// Example: "PHP 8.3.13 (cli) (built: Nov 26 2024 12:00:00) (NTS Visual
// C++ 2019 x64)". A thread-safe build reports "(TS ..." instead.
func ParsePHPVersionOutput(out string) (full string, majorMinor string, nts bool, arch string, err error) {
	first := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	m := phpVersionOutputRe.FindStringSubmatch(first)
	if m == nil {
		return "", "", false, "", fmt.Errorf("cannot parse php -v output: %q", first)
	}
	full = m[1] + "." + m[2] + "." + m[3]
	majorMinor = m[1] + "." + m[2]
	nts = strings.Contains(first, "NTS")
	switch {
	case strings.Contains(first, "x64"):
		arch = "x64"
	case strings.Contains(first, "arm64"):
		arch = "arm64"
	case strings.Contains(first, "x86"):
		arch = "x86"
	}
	return full, majorMinor, nts, arch, nil
}

// ParsePHPModules parses `php.exe -m` output: module names between
// the [PHP Modules] and [Zend Modules] headers.
func ParsePHPModules(out string) []string {
	var mods []string
	section := ""
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch t {
		case "[PHP Modules]":
			section = "php"
			continue
		case "[Zend Modules]":
			section = "zend"
			continue
		}
		if t == "" {
			continue
		}
		if section == "php" {
			mods = append(mods, t)
		}
	}
	return mods
}
