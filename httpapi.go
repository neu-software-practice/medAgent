package medagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", s.hStart)
	mux.HandleFunc("POST /sessions/{id}/patient-say", s.hPatientSay)
	mux.HandleFunc("POST /sessions/{id}/test-results", s.hTestResults)
	mux.HandleFunc("POST /sessions/{id}/purchase-result", s.hPurchaseResult)
	mux.HandleFunc("POST /sessions/{id}/drug-info", s.hDrugInfo)
	mux.HandleFunc("POST /sessions/{id}/vitals", s.hVitals)
	mux.HandleFunc("GET /sessions/{id}/record", s.hRecord)
	mux.HandleFunc("DELETE /sessions/{id}", s.hEnd)
	return mux
}

func (s *Service) hStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile map[string]any  `json:"profile"`
		Initial bool            `json:"initial"`
		Prior   []SessionRecord `json:"prior"`
	}
	if !decode(w, r, &body) {
		return
	}
	id, err := s.Start(body.Profile, body.Initial, body.Prior)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"session_id": id})
}

func (s *Service) hPatientSay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.PatientSay(r.Context(), r.PathValue("id"), body.Message)
	respondStep(w, step, err)
}

func (s *Service) hTestResults(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Results []TestResult `json:"results"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.SupplyTestResults(r.Context(), r.PathValue("id"), body.Results)
	respondStep(w, step, err)
}

func (s *Service) hPurchaseResult(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Results []DrugPurchase `json:"results"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.SupplyPurchaseResult(r.Context(), r.PathValue("id"), body.Results)
	respondStep(w, step, err)
}

func (s *Service) hDrugInfo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Infos []DrugInfo `json:"infos"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.SupplyDrugInfo(r.Context(), r.PathValue("id"), body.Infos)
	respondStep(w, step, err)
}

func (s *Service) hVitals(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Vitals map[string]any `json:"vitals"`
	}
	if !decode(w, r, &body) {
		return
	}
	step, err := s.ReportVitals(r.Context(), r.PathValue("id"), body.Vitals)
	respondStep(w, step, err)
}

func (s *Service) hRecord(w http.ResponseWriter, r *http.Request) {
	rec, err := s.Export(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, rec)
}

func (s *Service) hEnd(w http.ResponseWriter, r *http.Request) {
	s.End(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return false
	}
	return true
}

func respondStep(w http.ResponseWriter, step Step, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, step)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, ErrSessionNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrSessionClosed), errors.Is(err, ErrWrongStep):
		status = http.StatusConflict
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
