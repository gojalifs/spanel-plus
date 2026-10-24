// Package buildinfo holds build-time identity of the panel binary.
// Values can be overridden at build time with -ldflags -X.
package buildinfo

var (
	// Version is the panel release version (M1 = first Go skeleton;
	// 0.2.0-m1 adds the desktop WebView2 mode).
	Version = "0.2.0-m1"
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
