package core

import (
	"path/filepath"
	"testing"
)

func TestParseApacheListenPort(t *testing.T) {
	cases := []struct {
		name string
		conf string
		port int
		ok   bool
	}{
		{"bare port", "Listen 8000\n", 8000, true},
		{"ipv4 bind", "Listen 127.0.0.1:8000\n", 8000, true},
		{"ipv6 bind", "Listen [::1]:8000\n", 8000, true},
		{"port 80", "Listen 80\n", 80, true},
		{"with protocol", "Listen 8000 https\n", 8000, true},
		{"trailing comment", "Listen 8000 # panel\n", 8000, true},
		{"leading whitespace", "  Listen 8000\n", 8000, true},
		{"lowercase directive", "listen 8000\n", 8000, true},
		{"first wins", "Listen 8000\nListen 9000\n", 8000, true},
		{"commented ignored", "#Listen 80\nListen 8000\n", 8000, true},
		{"indented comment ignored", "  # Listen 80\nListen 8000\n", 8000, true},
		{"no listen", "ServerName localhost\n", 0, false},
		{"empty", "", 0, false},
		{"port zero", "Listen 0\n", 0, false},
		{"port too big", "Listen 99999\n", 0, false},
		{"listen without port", "Listen\n", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			port, ok := ParseApacheListenPort(c.conf)
			if port != c.port || ok != c.ok {
				t.Errorf("ParseApacheListenPort(%q) = (%d, %v), want (%d, %v)", c.conf, port, ok, c.port, c.ok)
			}
		})
	}
}

func TestDetectApachePort(t *testing.T) {
	fs := newMemFS()
	confPath := filepath.Join("C:/m0/Apache24", "conf", "httpd.conf")
	if err := fs.WriteFile(confPath, []byte("Listen 8000\n")); err != nil {
		t.Fatal(err)
	}
	if got := DetectApachePort(fs, "C:/m0/Apache24"); got != 8000 {
		t.Errorf("conf with Listen 8000: got %d, want 8000", got)
	}
	// Missing file, empty home, and a conf without Listen all fall
	// back to the historic default instead of failing the panel.
	if got := DetectApachePort(fs, "C:/nowhere"); got != DefaultApachePort {
		t.Errorf("missing conf: got %d, want %d", got, DefaultApachePort)
	}
	if got := DetectApachePort(fs, ""); got != DefaultApachePort {
		t.Errorf("empty home: got %d, want %d", got, DefaultApachePort)
	}
	emptyHome := "C:/empty"
	if err := fs.WriteFile(filepath.Join(emptyHome, "conf", "httpd.conf"), []byte("ServerName localhost\n")); err != nil {
		t.Fatal(err)
	}
	if got := DetectApachePort(fs, emptyHome); got != DefaultApachePort {
		t.Errorf("conf without Listen: got %d, want %d", got, DefaultApachePort)
	}
}

func TestSiteBaseURL(t *testing.T) {
	if got := SiteBaseURL(80); got != "http://127.0.0.1" {
		t.Errorf("port 80: got %q", got)
	}
	if got := SiteBaseURL(8000); got != "http://127.0.0.1:8000" {
		t.Errorf("port 8000: got %q", got)
	}
}
