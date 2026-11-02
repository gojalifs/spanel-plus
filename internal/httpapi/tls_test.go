package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

// TLS API tests use the shared apiHandler harness (no auth service
// attached, so the middleware lets the calls through like degraded
// mode; the auth gate itself is covered by the M5 tests).
func TestTLSAPIStatusInitDownloadRenewReinit(t *testing.T) {
	h, _ := apiHandler(t)

	rec := call(t, h, "GET", "/api/v1/tls/status", "")
	if rec.Code != 200 {
		t.Fatalf("status tanpa material: %d %s", rec.Code, rec.Body.String())
	}
	var st map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st["enabled"] != false {
		t.Fatalf("status tanpa material harus enabled=false: %v", st)
	}

	rec = call(t, h, "GET", "/api/v1/tls/ca.crt", "")
	if rec.Code != 404 {
		t.Fatalf("ca.crt tanpa material harus 404, dapat %d", rec.Code)
	}

	rec = call(t, h, "POST", "/api/v1/tls/init", `{"ips":["192.168.89.21"],"ca_years":100}`)
	if rec.Code != 200 {
		t.Fatalf("init: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st["enabled"] != true || st["ca_fingerprint"] == "" {
		t.Fatalf("init harus enabled + fingerprint: %v", st)
	}

	rec = call(t, h, "GET", "/api/v1/tls/ca.crt", "")
	if rec.Code != 200 {
		t.Fatalf("ca.crt sesudah init: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-x509-ca-cert" {
		t.Errorf("ca.crt content-type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "spanel-ca.crt") {
		t.Errorf("ca.crt disposition = %q", cd)
	}
	if !strings.HasPrefix(rec.Body.String(), "-----BEGIN CERTIFICATE-----") {
		t.Errorf("ca.crt body bukan PEM")
	}

	rec = call(t, h, "POST", "/api/v1/tls/renew", `{}`)
	if rec.Code != 200 {
		t.Fatalf("renew: %d %s", rec.Code, rec.Body.String())
	}
	var renew map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &renew); err != nil {
		t.Fatal(err)
	}
	if renew["changed"] != false {
		t.Errorf("renew leaf baru harus changed=false: %v", renew)
	}

	rec = call(t, h, "POST", "/api/v1/tls/reinit-ca", `{}`)
	if rec.Code != 400 {
		t.Fatalf("reinit tanpa confirm harus 400, dapat %d", rec.Code)
	}

	oldFP := st["ca_fingerprint"]
	rec = call(t, h, "POST", "/api/v1/tls/reinit-ca", `{"confirm":true}`)
	if rec.Code != 200 {
		t.Fatalf("reinit confirm: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st["ca_fingerprint"] == oldFP {
		t.Errorf("reinit harus mengganti fingerprint CA")
	}

	rec = call(t, h, "POST", "/api/v1/tls/regenerate-leaf", `{"ips":["10.0.0.9"]}`)
	if rec.Code != 200 {
		t.Fatalf("regenerate: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	ips, _ := st["leaf_ips"].([]any)
	found := false
	for _, ip := range ips {
		if ip == "10.0.0.9" {
			found = true
		}
	}
	if !found {
		t.Errorf("regenerate harus mengganti SAN: %v", st["leaf_ips"])
	}
}
