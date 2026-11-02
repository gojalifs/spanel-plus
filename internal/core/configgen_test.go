package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenFixture is the registry the golden files under
// testdata/golden were rendered from. Changing it (or the generator)
// breaks the golden test on purpose: review the diff, and only then
// regenerate the goldens.
func goldenFixture() Registry {
	return Registry{
		Settings: Settings{ApacheHome: `C:\m0\Apache24`, ApacheService: "Apache2.4", DocumentRoot: `C:\m0\htdocs`, PanelPort: 8888},
		PHPVersions: []PHPVersion{
			{Version: "8.2", Path: `C:\panel\php\8.2`},
			{Version: "8.3", Path: `C:\panel\php\8.3`},
		},
		Projects: []Project{
			{Name: "app-laravel", URLPath: "/app-laravel", FSPath: `C:\m0\htdocs\app-laravel`, Type: ProjectLaravel, PublicSubdir: "public", PHPVersion: "8.2", Enabled: true},
			{Name: "app-ci4", URLPath: "/app-ci4", FSPath: `C:\m0\htdocs\app-ci4`, Type: ProjectCI4, PublicSubdir: "public", PHPVersion: "8.2", Enabled: true},
			{Name: "app-php82", URLPath: "/app-php82", FSPath: `C:\m0\htdocs\app-php82`, Type: ProjectPHPNative, PHPVersion: "8.2", Enabled: true},
			{Name: "alat", URLPath: "/alat", FSPath: `D:\apps\alat`, Type: ProjectPHPNative, PHPVersion: "8.3", Enabled: true},
			{Name: "dokumen", URLPath: "/dokumen", FSPath: `C:\m0\htdocs\dokumen`, Type: ProjectStatic, PublicSubdir: "dist", Enabled: true},
			{Name: "mati", URLPath: "/mati", FSPath: `C:\m0\htdocs\mati`, Type: ProjectPHPNative, PHPVersion: "8.2", Enabled: false},
		},
	}
}

func TestConfigGenGoldenFiles(t *testing.T) {
	out, err := Generator{}.GenerateAll(goldenFixture())
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	goldenDir := filepath.Join("testdata", "golden")
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatalf("read golden dir: %v", err)
	}
	if len(out) != len(entries) {
		t.Fatalf("generated %d files, golden dir has %d — file set changed", len(out), len(entries))
	}
	for _, e := range entries {
		want, err := os.ReadFile(filepath.Join(goldenDir, e.Name()))
		if err != nil {
			t.Fatalf("read golden %s: %v", e.Name(), err)
		}
		got, ok := out[e.Name()]
		if !ok {
			t.Errorf("generator did not produce golden file %s", e.Name())
			continue
		}
		if got != string(want) {
			t.Errorf("golden mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", e.Name(), got, want)
		}
	}
	if _, ok := out["mati.conf"]; ok {
		t.Error("disabled project must not generate a config file")
	}
}

func TestConfigGenDeterministic(t *testing.T) {
	reg := goldenFixture()
	first, err := Generator{}.GenerateAll(reg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Generator{}.GenerateAll(reg)
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != len(first) {
			t.Fatal("file count changed between runs")
		}
		for name, content := range first {
			if again[name] != content {
				t.Fatalf("output for %s is not deterministic", name)
			}
		}
	}
}

func TestConfigGenRejectsInvalidRegistry(t *testing.T) {
	reg := goldenFixture()
	reg.Projects[0].PHPVersion = "9.9" // unregistered
	if _, err := (Generator{}).GenerateAll(reg); err == nil {
		t.Fatal("GenerateAll must refuse an invalid registry")
	}
}

func TestConfigGenTLSVHost(t *testing.T) {
	// Tanpa TLS: tidak ada Listen tambahan maupun VirtualHost.
	out, err := Generator{}.GenerateAll(goldenFixture())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out[GlobalConfigName], "VirtualHost") {
		t.Errorf("tanpa TLS, global tidak boleh memuat VirtualHost")
	}
	// Dengan TLS: Listen + VirtualHost persis, path slash.
	out, err = Generator{TLS: TLSVHost{
		Enabled:  true,
		Port:     8443,
		CertFile: `C:\panel\data\tls\server.crt`,
		KeyFile:  `C:\panel\data\tls\server.key`,
	}}.GenerateAll(goldenFixture())
	if err != nil {
		t.Fatal(err)
	}
	global := out[GlobalConfigName]
	for _, want := range []string{
		"Listen 8443",
		"<VirtualHost *:8443>",
		"SSLEngine on",
		`SSLCertificateFile "C:/panel/data/tls/server.crt"`,
		`SSLCertificateKeyFile "C:/panel/data/tls/server.key"`,
		"SSLProtocol -all +TLSv1.2 +TLSv1.3",
		"</VirtualHost>",
	} {
		if !strings.Contains(global, want) {
			t.Errorf("global harus memuat %q, dapat:\n%s", want, global)
		}
	}
	// Proyek lain tidak berubah oleh vhost.
	if !strings.Contains(out["app-laravel.conf"], "app-laravel") {
		t.Errorf("file proyek harus tetap ter-render")
	}
}
