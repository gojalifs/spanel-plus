package platform

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// ExecPHPProber implements core.PHPProber by running the folder's
// php.exe (-v for the build flavor, -m for the module list).
type ExecPHPProber struct {
	Runner core.CommandRunner
}

func (p ExecPHPProber) Probe(ctx context.Context, dir string) (core.PHPProbeResult, error) {
	runner := p.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	exe := filepath.Join(dir, "php.exe")
	vOut, err := runner.Run(ctx, exe, "-v")
	if err != nil {
		return core.PHPProbeResult{}, fmt.Errorf("run php -v in %s: %w", dir, err)
	}
	full, majorMinor, nts, arch, err := core.ParsePHPVersionOutput(vOut)
	if err != nil {
		return core.PHPProbeResult{}, err
	}
	mOut, err := runner.Run(ctx, exe, "-m")
	if err != nil {
		return core.PHPProbeResult{}, fmt.Errorf("run php -m in %s: %w", dir, err)
	}
	return core.PHPProbeResult{
		Version: majorMinor,
		Full:    full,
		NTS:     nts,
		Arch:    arch,
		Modules: core.ParsePHPModules(mOut),
	}, nil
}
