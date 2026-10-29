// Apache site port detection: the panel's health checks and the
// dashboard's project links must target the port Apache actually
// listens on, not a hardcoded 80. The single source of truth is the
// first Listen directive in httpd.conf — the same source the setup
// and deploy scripts already use. panel.json deliberately carries
// no apache_port field, so the config can never drift from the
// file Apache itself reads.
package core

import (
	"path/filepath"
	"regexp"
	"strconv"
)

// DefaultApachePort is Apache's own default and the panel's historic
// behaviour: when httpd.conf cannot be read or carries no usable
// Listen directive, the panel probes port 80 exactly as before.
const DefaultApachePort = 80

// listenRe matches one Listen directive: "Listen 8000",
// "Listen 127.0.0.1:8000", "Listen [::1]:8000", "Listen 8000 https".
// (?i) because Apache directives are case-insensitive; (?m) so ^
// anchors at each line. A commented line ("#Listen 80") never
// matches: after optional whitespace the next character must be L.
var listenRe = regexp.MustCompile(`(?im)^\s*Listen\s+(?:\S+:)?(\d+)`)

// ParseApacheListenPort returns the port from the first Listen
// directive in an httpd.conf text, and false when there is none or
// the first one is out of range. The first directive wins: Apache
// listens on every Listen port, so any one of them answers the
// health check, and the first is what the scripts use too.
func ParseApacheListenPort(conf string) (int, bool) {
	m := listenRe.FindStringSubmatch(conf)
	if m == nil {
		return 0, false
	}
	port, err := strconv.Atoi(m[1])
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

// DetectApachePort reads <apacheHome>/conf/httpd.conf and returns
// its Listen port. Any failure (empty apacheHome, missing file,
// unreadable file, no usable Listen) returns DefaultApachePort, so
// a broken Apache setup keeps the old behaviour instead of failing
// the panel itself — the apply will still fail loudly at httpd -t
// or the restart step when Apache really is broken.
func DetectApachePort(files FileStore, apacheHome string) int {
	if apacheHome == "" {
		return DefaultApachePort
	}
	raw, err := files.ReadFile(filepath.Join(apacheHome, "conf", "httpd.conf"))
	if err != nil {
		return DefaultApachePort
	}
	if port, ok := ParseApacheListenPort(string(raw)); ok {
		return port
	}
	return DefaultApachePort
}

// SiteBaseURL builds the base URL health checks probe: port 80
// keeps the historic bare form (http://127.0.0.1), any other port
// is explicit (http://127.0.0.1:8000).
func SiteBaseURL(port int) string {
	if port == DefaultApachePort {
		return "http://127.0.0.1"
	}
	return "http://127.0.0.1:" + strconv.Itoa(port)
}
