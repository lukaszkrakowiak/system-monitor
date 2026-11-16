package api

import (
	"net/http"
	"time"

	"server-monitor/internal/storage"
)

// maxIPLabels ogranicza liczbę etykiet (ochrona bazy przed rozrostem).
const maxIPLabels = 1000

// IPLabelStore to zależność endpointów etykiet adresów IP.
type IPLabelStore interface {
	List() ([]storage.IPLabel, error)
	Set(ip, label string, now time.Time) error
	Delete(ip string) error
}

// IPLabelDTO to etykieta nadana adresowi IP.
type IPLabelDTO struct {
	IP    string `json:"ip"`
	Label string `json:"label"`
}

// IPLabelsResponse to odpowiedź GET /api/ip-labels.
type IPLabelsResponse struct {
	Labels []IPLabelDTO `json:"labels"`
}

// IPLabelRequest to body PUT /api/ip-labels/{ip}.
type IPLabelRequest struct {
	Label string `json:"label"`
}

func (s *server) handleListIPLabels(w http.ResponseWriter, r *http.Request) {
	if s.alertsUnavailable(w, s.cfg.IPLabels != nil) {
		return
	}
	rows, err := s.cfg.IPLabels.List()
	if err != nil {
		s.internal(w, "list ip labels", err)
		return
	}
	out := IPLabelsResponse{Labels: make([]IPLabelDTO, 0, len(rows))}
	for _, l := range rows {
		out.Labels = append(out.Labels, IPLabelDTO{IP: l.IP, Label: l.Label})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handlePutIPLabel(w http.ResponseWriter, r *http.Request) {
	if s.alertsUnavailable(w, s.cfg.IPLabels != nil) {
		return
	}
	ip, ok := storage.NormalizeIP(r.PathValue("ip"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid IP address")
		return
	}
	var in IPLabelRequest
	if !readJSON(w, r, &in) {
		return
	}
	label, err := storage.CleanIPLabel(in.Label)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.cfg.IPLabels.List()
	if err != nil {
		s.internal(w, "list ip labels", err)
		return
	}
	known := false
	for _, l := range existing {
		if l.IP == ip {
			known = true
			break
		}
	}
	if !known && len(existing) >= maxIPLabels {
		writeError(w, http.StatusBadRequest, "too many labels")
		return
	}
	if err := s.cfg.IPLabels.Set(ip, label, s.now()); err != nil {
		s.internal(w, "save ip label", err)
		return
	}
	writeJSON(w, http.StatusOK, IPLabelDTO{IP: ip, Label: label})
}

// handleDeleteIPLabel jest idempotentne: usunięcie nieistniejącej etykiety też zwraca 204.
func (s *server) handleDeleteIPLabel(w http.ResponseWriter, r *http.Request) {
	if s.alertsUnavailable(w, s.cfg.IPLabels != nil) {
		return
	}
	ip, ok := storage.NormalizeIP(r.PathValue("ip"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid IP address")
		return
	}
	if err := s.cfg.IPLabels.Delete(ip); err != nil {
		s.internal(w, "delete ip label", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
