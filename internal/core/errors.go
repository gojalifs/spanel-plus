package core

import "errors"

// Sentinel errors for the registry/project domain. Callers (HTTP API,
// CLI) classify failures with errors.Is instead of matching message
// text; the wrapped messages stay human-readable for the terminal.
var (
	// ErrProjectNotFound marks an operation naming an unknown project.
	ErrProjectNotFound = errors.New("project not found")
	// ErrProjectExists marks adding a project whose name is taken.
	ErrProjectExists = errors.New("project already exists")
	// ErrPHPVersionNotRegistered marks referencing an unregistered
	// PHP version.
	ErrPHPVersionNotRegistered = errors.New("php version is not registered")
	// ErrPHPVersionExists marks registering the same version twice.
	ErrPHPVersionExists = errors.New("php version already registered")
	// ErrPHPVersionInUse marks removing a PHP version projects use.
	ErrPHPVersionInUse = errors.New("php version still in use")
	// ErrProcessNotFound marks an unknown CLI process definition.
	ErrProcessNotFound = errors.New("cli process not found")
)
