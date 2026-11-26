package core

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Apache Lounge bundle fetch (Opsi A, sisi PC dev yang berinternet).
//
// The dev PC downloads the Apache Lounge httpd zip + the matching
// mod_fcgid zip and packs them into ONE bundle file with a SHA-256
// manifest. The bundle is then copied to the offline server (USB,
// LAN share — anything) and imported there; the server never touches
// the internet.
//
// Pinned facts (verified against the live download page 2026-11-26):
//   - VS is pinned to VS18: every Win64 httpd build on the page is
//     VS18, and mod_fcgid must be the same VS or it refuses to load.
//   - mod_fcgid is pinned to 2.3.10 (the only version listed).
//   - The httpd filename carries a build-date segment between the
//     version and Win64 (httpd-2.4.71-261112-Win64-VS18.zip), so a
//     pinned version can only be found by reading the page listing,
//     never by constructing the URL from the version alone.
//   - Apache Lounge's bot protection rejects the default Go
//     User-Agent (observed HTTP 403 on 2026-11-26), so the fetcher
//     sends a browser User-Agent like Invoke-WebRequest does.

const (
	// ApacheLoungeBaseURL is the download index the fetcher scrapes.
	ApacheLoungeBaseURL = "https://www.apachelounge.com/download/"
	// ApacheVS is the Visual Studio toolchain generation. Pinned:
	// mod_fcgid only exists for the same VS as the httpd build.
	ApacheVS = "VS18"
	// ApacheFcgidVersion is the mod_fcgid release bundled with
	// every Apache upgrade (the only version Apache Lounge lists).
	ApacheFcgidVersion = "2.3.10"
	// ApacheBundleFormat is the manifest format version. Import
	// rejects any other value so an old bundle can never be
	// mistaken for a new one.
	ApacheBundleFormat = 1
)

// apacheBundleFileRe matches one httpd Win64 zip name on the
// download page, e.g. httpd-2.4.71-261112-Win64-VS18.zip.
var apacheBundleFileRe = regexp.MustCompile(`httpd-2\.4\.[0-9]+-[0-9]+-Win64-VS18\.zip`)

// apacheBundleVersionRe extracts the patch version from a matched
// filename.
var apacheBundleVersionRe = regexp.MustCompile(`^httpd-(\d+\.\d+\.\d+)-[0-9]+-Win64-VS18\.zip$`)

// ApacheBundleManifest is manifest.json inside every bundle. The
// import side verifies BOTH inner zips against these hashes before
// touching the running Apache, so a bundle corrupted in transit
// (bad USB copy, truncated download) fails loud instead of
// installing half an Apache.
type ApacheBundleManifest struct {
	FormatVersion  int       `json:"format_version"`
	ApacheVersion  string    `json:"apache_version"`
	ApacheVS       string    `json:"apache_vs"`
	ApacheFilename string    `json:"apache_filename"`
	ApacheSHA256   string    `json:"apache_sha256"`
	ApacheURL      string    `json:"apache_url"`
	FcgidVersion   string    `json:"fcgid_version"`
	FcgidSHA256    string    `json:"fcgid_sha256"`
	FcgidURL       string    `json:"fcgid_url"`
	CreatedAt      time.Time `json:"created_at"`
}

// ApacheFetcher downloads Apache Lounge releases. HTTP and BaseURL
// are injectable so tests run against a local httptest server
// instead of the internet.
type ApacheFetcher struct {
	HTTP    *http.Client // nil = default (30s timeout)
	BaseURL string       // "" = ApacheLoungeBaseURL
}

func (f *ApacheFetcher) base() string {
	if f.BaseURL != "" {
		return f.BaseURL
	}
	return ApacheLoungeBaseURL
}

func (f *ApacheFetcher) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (f *ApacheFetcher) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// Browser User-Agent: Apache Lounge rejects the default Go
	// User-Agent with HTTP 403 (observed 2026-11-26).
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	return body, nil
}

// Discover lists the httpd Win64 VS18 filenames on the download
// page, newest first as listed there.
func (f *ApacheFetcher) Discover(ctx context.Context) ([]string, error) {
	page, err := f.get(ctx, f.base())
	if err != nil {
		return nil, fmt.Errorf("tidak bisa membaca halaman download Apache Lounge: %w", err)
	}
	found := apacheBundleFileRe.FindAllString(string(page), -1)
	seen := map[string]bool{}
	var out []string
	for _, name := range found {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tidak ada file httpd-2.4.*-Win64-VS18.zip di %s — struktur halaman berubah, laporkan ke Muse", f.base())
	}
	return out, nil
}

// parseApacheBundleFilename extracts the patch version from a
// download-page filename (httpd-2.4.71-261112-Win64-VS18.zip -> 2.4.71).
func parseApacheBundleFilename(name string) (string, error) {
	m := apacheBundleVersionRe.FindStringSubmatch(name)
	if m == nil {
		return "", fmt.Errorf("nama file Apache tidak dikenal: %q", name)
	}
	return m[1], nil
}

// FetchBundle downloads the httpd zip for version ("" = latest on
// the page) plus the matching mod_fcgid zip, and writes the bundle
// (manifest.json + apache.zip + mod_fcgid.zip) to w. It returns the
// manifest so the caller can print the bundle's own SHA-256 and
// the versions it carries.
func (f *ApacheFetcher) FetchBundle(ctx context.Context, version string, w io.Writer) (ApacheBundleManifest, error) {
	files, err := f.Discover(ctx)
	if err != nil {
		return ApacheBundleManifest{}, err
	}
	filename := ""
	if version == "" {
		filename = files[0]
		version, err = parseApacheBundleFilename(filename)
		if err != nil {
			return ApacheBundleManifest{}, err
		}
	} else {
		var available []string
		for _, name := range files {
			v, err := parseApacheBundleFilename(name)
			if err != nil {
				continue
			}
			available = append(available, v)
			if v == version {
				filename = name
			}
		}
		if filename == "" {
			return ApacheBundleManifest{}, fmt.Errorf("Apache %s tidak ada di halaman download — yang tersedia: %s", version, strings.Join(available, ", "))
		}
	}
	base := f.base()
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	apacheURL := base + ApacheVS + "/binaries/" + filename
	fcgidFilename := "mod_fcgid-" + ApacheFcgidVersion + "-win64-" + ApacheVS + ".zip"
	fcgidURL := base + ApacheVS + "/modules/" + fcgidFilename

	apacheZip, err := f.get(ctx, apacheURL)
	if err != nil {
		return ApacheBundleManifest{}, fmt.Errorf("download Apache %s gagal: %w", version, err)
	}
	fcgidZip, err := f.get(ctx, fcgidURL)
	if err != nil {
		return ApacheBundleManifest{}, fmt.Errorf("download mod_fcgid %s gagal: %w", ApacheFcgidVersion, err)
	}
	manifest := ApacheBundleManifest{
		FormatVersion:  ApacheBundleFormat,
		ApacheVersion:  version,
		ApacheVS:       ApacheVS,
		ApacheFilename: filename,
		ApacheSHA256:   sha256Hex(apacheZip),
		ApacheURL:      apacheURL,
		FcgidVersion:   ApacheFcgidVersion,
		FcgidSHA256:    sha256Hex(fcgidZip),
		FcgidURL:       fcgidURL,
		CreatedAt:      time.Now().UTC(),
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ApacheBundleManifest{}, err
	}
	zw := zip.NewWriter(w)
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{"manifest.json", manifestJSON},
		{"apache.zip", apacheZip},
		{"mod_fcgid.zip", fcgidZip},
	} {
		fw, err := zw.Create(entry.name)
		if err != nil {
			return ApacheBundleManifest{}, err
		}
		if _, err := fw.Write(entry.data); err != nil {
			return ApacheBundleManifest{}, err
		}
	}
	if err := zw.Close(); err != nil {
		return ApacheBundleManifest{}, err
	}
	return manifest, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
