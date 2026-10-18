package announcement

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type HTTPAPI struct {
	service *Service
}

func NewHTTPHandler(service *Service) http.Handler {
	api := &HTTPAPI{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tenants", api.onboardTenant)
	mux.HandleFunc("PUT /tenants/{tenantID}/accounts/{accountID}", api.setAccount)
	mux.HandleFunc("POST /admin/announcements", api.broadcast)
	return mux
}

func (a *HTTPAPI) onboardTenant(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TenantID string `json:"tenant_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	tenant, err := a.service.OnboardTenant(r.Context(), input.TenantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, tenant)
}

func (a *HTTPAPI) setAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status AccountStatus `json:"status"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	account, err := a.service.SetAccount(r.PathValue("tenantID"), r.PathValue("accountID"), input.Status)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (a *HTTPAPI) broadcast(w http.ResponseWriter, r *http.Request) {
	var notice Notice
	if !decodeJSON(w, r, &notice) {
		return
	}
	result, err := a.service.Broadcast(r.Context(), notice)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return false
	}
	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		writeJSON(w, apiErr.HTTPStatus, map[string]string{"error": apiErr.Code, "message": apiErr.Message})
		return
	}
	if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "must") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err.Error() == "tenant not found" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "announcement delivery failed"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
