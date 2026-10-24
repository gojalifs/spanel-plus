package core

import (
	"net/url"
	"strconv"
	"strings"
)

// Warning is one non-blocking migration warning on a project, shown
// in the dashboard so adoption problems surface before they become
// 500s (Plan F1 scanner & F4 APP_URL check).
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ProjectWarnings scans a project for the two known migration traps:
//   - Laravel APP_URL that does not match the panel URL path (or still
//     carries the /public suffix the panel removes), and
//   - .htaccess containing php_value/php_flag (mod_php directives that
//     become errors under mod_fcgid).
//
// Scanning is best-effort: an unreadable file simply yields no
// warning for that check (the project folder existence itself is
// enforced by registry environment validation, not here).
func ProjectWarnings(files FileStore, p Project) []Warning {
	var out []Warning
	if p.Type == ProjectLaravel {
		out = append(out, appURLWarning(files, p)...)
	}
	out = append(out, htaccessWarnings(files, p)...)
	return out
}

func appURLWarning(files FileStore, p Project) []Warning {
	raw, err := files.ReadFile(joinPath(p.FSPath, ".env"))
	if err != nil {
		return []Warning{{
			Code:    "app_url_missing",
			Message: "File .env tidak ditemukan di folder proyek — APP_URL tidak dapat diperiksa.",
		}}
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "APP_URL=") {
			continue
		}
		value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "APP_URL=")), `"'`)
		u, perr := url.Parse(value)
		path := value
		if perr == nil && u.Path != "" {
			path = u.Path
		}
		path = strings.TrimSuffix(path, "/")
		if path == "" {
			path = "/"
		}
		if path != p.URLPath {
			return []Warning{{
				Code: "app_url_mismatch",
				Message: "APP_URL proyek mengarah ke path '" + path + "', panel melayani '" + p.URLPath +
					"' (tanpa /public). Samakan APP_URL di .env agar URL & asset proyek benar.",
			}}
		}
		return nil
	}
	return []Warning{{
		Code:    "app_url_missing",
		Message: "APP_URL tidak ditemukan di .env proyek — periksa manual agar cocok dengan path panel '" + p.URLPath + "'.",
	}}
}

func htaccessWarnings(files FileStore, p Project) []Warning {
	candidates := []string{joinPath(p.TargetDir(), ".htaccess")}
	if p.TargetDir() != p.FSPath {
		candidates = append(candidates, joinPath(p.FSPath, ".htaccess"))
	}
	var out []Warning
	for _, path := range candidates {
		raw, err := files.ReadFile(path)
		if err != nil {
			continue
		}
		var bad []string
		for _, line := range strings.Split(string(raw), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "php_value") || strings.HasPrefix(t, "php_flag") {
				bad = append(bad, t)
			}
		}
		if len(bad) > 0 {
			out = append(out, Warning{
				Code: "php_value_in_htaccess",
				Message: ".htaccess di " + path + " memuat " + strconv.Itoa(len(bad)) +
					" direktif mod_php (" + bad[0] + ", …) — di bawah mod_fcgid ini jadi error 500; pindahkan ke php.ini/.user.ini.",
			})
		}
	}
	return out
}
