package core

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeLounge serves a download page plus the two zips, so fetch is
// proven without the internet.
type fakeLounge struct {
	page      string
	apacheZip []byte
	fcgidZip  []byte
}

func newFakeLounge(t *testing.T) *fakeLounge {
	t.Helper()
	mkzip := func(files map[string]string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, content := range files {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	return &fakeLounge{
		page: `<html><body>
			<a href="VS18/binaries/httpd-2.4.71-261112-Win64-VS18.zip">httpd-2.4.71-261112-Win64-VS18.zip</a>
			<a href="VS18/binaries/httpd-2.4.69-261002-Win64-VS18.zip">httpd-2.4.69-261002-Win64-VS18.zip</a>
			<a href="VS18/modules/mod_fcgid-2.3.10-win64-VS18.zip">mod_fcgid-2.3.10-win64-VS18.zip</a>
			</body></html>`,
		apacheZip: mkzip(map[string]string{"Apache24/bin/httpd.exe": "2.4.71"}),
		fcgidZip:  mkzip(map[string]string{"mod_fcgid.so": "so-bytes"}),
	}
}

func (f *fakeLounge) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/download/":
		_, _ = w.Write([]byte(f.page))
	case strings.Contains(r.URL.Path, "httpd-2.4.") && strings.HasSuffix(r.URL.Path, "-Win64-VS18.zip"):
		_, _ = w.Write(f.apacheZip)
	case strings.HasSuffix(r.URL.Path, "mod_fcgid-2.3.10-win64-VS18.zip"):
		_, _ = w.Write(f.fcgidZip)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestApacheFetcherDiscover(t *testing.T) {
	lounge := newFakeLounge(t)
	srv := httptest.NewServer(http.HandlerFunc(lounge.handler))
	defer srv.Close()
	fetcher := &ApacheFetcher{BaseURL: srv.URL + "/download/"}
	files, err := fetcher.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "httpd-2.4.71-261112-Win64-VS18.zip" {
		t.Errorf("Discover = %v, want 2.4.71 first", files)
	}
}

func TestApacheFetcherFetchBundle(t *testing.T) {
	lounge := newFakeLounge(t)
	srv := httptest.NewServer(http.HandlerFunc(lounge.handler))
	defer srv.Close()
	fetcher := &ApacheFetcher{BaseURL: srv.URL + "/download/"}

	var buf bytes.Buffer
	manifest, err := fetcher.FetchBundle(context.Background(), "", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ApacheVersion != "2.4.71" || manifest.ApacheVS != "VS18" || manifest.FcgidVersion != "2.3.10" {
		t.Errorf("manifest = %+v", manifest)
	}
	// The bundle must be a valid zip whose inner zips match the
	// manifest hashes — that is what import verifies.
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if _, err := b.ReadFrom(rc); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
		entries[f.Name] = b.Bytes()
	}
	var gotManifest ApacheBundleManifest
	if err := json.Unmarshal(entries["manifest.json"], &gotManifest); err != nil {
		t.Fatal(err)
	}
	if gotManifest.ApacheSHA256 != manifest.ApacheSHA256 {
		t.Error("manifest inside the bundle differs from the returned one")
	}
	for name, want := range map[string]string{"apache.zip": manifest.ApacheSHA256, "mod_fcgid.zip": manifest.FcgidSHA256} {
		sum := sha256.Sum256(entries[name])
		if hex.EncodeToString(sum[:]) != want {
			t.Errorf("%s hash mismatch", name)
		}
	}

	// A pinned version picks that release, not the latest.
	buf.Reset()
	manifest, err = fetcher.FetchBundle(context.Background(), "2.4.69", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ApacheVersion != "2.4.69" {
		t.Errorf("pinned fetch = %s, want 2.4.69", manifest.ApacheVersion)
	}

	// An unknown version fails loud and names what exists.
	if _, err := fetcher.FetchBundle(context.Background(), "2.4.99", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "2.4.71") {
		t.Errorf("unknown version must error naming available versions, got %v", err)
	}
}

func TestParseApacheBundleFilename(t *testing.T) {
	if v, err := parseApacheBundleFilename("httpd-2.4.71-261112-Win64-VS18.zip"); err != nil || v != "2.4.71" {
		t.Errorf("parse = %q, %v", v, err)
	}
	if _, err := parseApacheBundleFilename("httpd-2.4.71-Win64-VS18.zip"); err == nil {
		t.Error("filename without the date segment must fail (the segment is real)")
	}
}
