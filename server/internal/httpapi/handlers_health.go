package httpapi

import (
	"net/http"
	"time"
)

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	problems := s.Cfg.Gate()
	status := "ok"
	if len(problems) > 0 {
		status = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          status,
		"service":         "touch-server",
		"app_id":          s.Cfg.AppID,
		"env":             s.Cfg.Env,
		"features":        map[string]bool{"upload": s.Cfg.FeatureUpload, "notify": s.Cfg.FeatureNotify, "task": s.Cfg.FeatureTask},
		"config_problems": problems,
	})
}

func parseRFC3339(v string) (time.Time, error) {
	return time.Parse(time.RFC3339, v)
}
