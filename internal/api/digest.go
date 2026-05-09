package api

import (
	"encoding/json"
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/digest"
)

func (s *Server) serveDigestTrigger(w http.ResponseWriter, r *http.Request) {
	if s.digestConfig == nil || s.digestStore == nil {
		http.Error(w, "digest not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		To string `json:"to"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}

	cfg := *s.digestConfig
	notifiers := digest.BuildNotifiers(cfg)

	if req.To != "" {
		mailCfg := cfg.Mail
		mailCfg.To = req.To
		mailCfg.Enabled = true
		notifiers = []digest.Notifier{&digest.EmailNotifier{Cfg: mailCfg}}
	}

	if len(notifiers) == 0 {
		http.Error(w, "no notifiers configured", http.StatusBadRequest)
		return
	}

	errs := digest.Send(r.Context(), cfg, s.digestStore, notifiers)
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"errors": msgs})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}
