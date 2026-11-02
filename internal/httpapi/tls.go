package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/gojalifs/spanel-plus/internal/app"
)

// TLS API (HTTPS v1): status, lifecycle actions, and the public CA
// download. All of these sit under /api/, so the auth middleware in
// server.go gates them to admin sessions exactly like every other
// state endpoint — including ca.crt (the cert is public, but the
// download button is an admin affordance, not a public URL).

func (a *api) tlsStatus(w http.ResponseWriter, _ *http.Request) {
	info, err := a.application.TLSInfo()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

type tlsInitBody struct {
	IPs      []string `json:"ips"`
	Names    []string `json:"names"`
	CAYears  int      `json:"ca_years"`
	LeafDays int      `json:"leaf_days"`
}

func (a *api) tlsInit(w http.ResponseWriter, r *http.Request) {
	var body tlsInitBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, err)
		return
	}
	info, err := a.application.TLSInit(body.IPs, body.Names, body.CAYears, body.LeafDays)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// tlsActionResponse merges the fresh TLS status with the action
// outcome (the status's leaf_expiry is already post-action).
type tlsActionResponse struct {
	app.TLSInfo
	Changed         bool   `json:"changed"`
	ApacheRestarted bool   `json:"apache_restarted"`
	ApacheError     string `json:"apache_error,omitempty"`
}

func (a *api) writeTLSAction(w http.ResponseWriter, res app.TLSRenewResult, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	info, err := a.application.TLSInfo()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tlsActionResponse{
		TLSInfo:         info,
		Changed:         res.Changed,
		ApacheRestarted: res.ApacheRestarted,
		ApacheError:     res.ApacheError,
	})
}

func (a *api) tlsRenew(w http.ResponseWriter, _ *http.Request) {
	res, err := a.application.TLSRenew()
	a.writeTLSAction(w, res, err)
}

type tlsSANBody struct {
	IPs   []string `json:"ips"`
	Names []string `json:"names"`
}

func (a *api) tlsRegenerateLeaf(w http.ResponseWriter, r *http.Request) {
	var body tlsSANBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, err)
		return
	}
	res, err := a.application.TLSRegenerateLeaf(body.IPs, body.Names)
	a.writeTLSAction(w, res, err)
}

type tlsReinitBody struct {
	tlsSANBody
	Confirm bool `json:"confirm"`
	CAYears int  `json:"ca_years"`
}

func (a *api) tlsReinitCA(w http.ResponseWriter, r *http.Request) {
	var body tlsReinitBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, err)
		return
	}
	if !body.Confirm {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "reinit-ca wajib confirm=true — ini mengganti trust anchor; semua client harus install ulang ca.crt",
		})
		return
	}
	res, err := a.application.TLSReinitCA(body.IPs, body.Names, body.CAYears)
	a.writeTLSAction(w, res, err)
}

func (a *api) tlsCACert(w http.ResponseWriter, _ *http.Request) {
	raw, err := a.application.TLSCACert()
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "ca.crt tidak tersedia — TLS belum aktif"})
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="spanel-ca.crt"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
