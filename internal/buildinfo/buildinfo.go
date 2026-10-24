// Package buildinfo holds build-time identity of the panel binary.
// Values can be overridden at build time with -ldflags -X.
package buildinfo

var (
	// Version is the panel release version (M1 = first Go skeleton;
	// 0.2.0-m1 adds the desktop WebView2 mode; 0.3.0-m2 adds the
	// registry + ConfigGen + apply pipeline; 0.4.0-m3 adds the PHP
	// manager, project CRUD API and the dashboard UI; 0.5.0-m4 adds
	// the process Supervisor and the schedule:run Scheduler;
	// 0.6.0-m5 adds admin auth + audit log, the comprehensive
	// doctor, and CLI parity through the HTTP API).
	Version = "0.6.0-m5"
	// Commit is the git commit the binary was built from, if known.
	Commit = "dev"
)

// Name is the product name shown in UI, logs and the Windows Service.
const Name = "SPanel+"

// BinaryName is the single executable name for both serve and CLI modes (Plan D6).
const BinaryName = "panel"

// ServiceName is the Windows Service internal name.
const ServiceName = "SPanelPlus"

// ServiceDisplayName is the Windows Service display name.
const ServiceDisplayName = "SPanel+ Panel Service"
