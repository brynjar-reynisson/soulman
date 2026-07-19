package httpserver

import (
	"encoding/json"
	"net/http"
	"time"

	"soulman/web-svc/reports"
)

func (s *Server) reportsLatest(w http.ResponseWriter, r *http.Request) {
	for i := 0; i < 7; i++ {
		date := time.Now().AddDate(0, 0, -i)
		content, found, err := reports.Read(s.cfg.ReportsRoot, date)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if found {
			writeReportJSON(w, date, content)
			return
		}
	}
	writeJSONError(w, http.StatusNotFound, "no report found in the last 7 days")
}

func (s *Server) reportsByDate(w http.ResponseWriter, r *http.Request) {
	dateStr := r.URL.Query().Get("date")
	date, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid or missing date, expected YYYY-MM-DD")
		return
	}

	content, found, err := reports.Read(s.cfg.ReportsRoot, date)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "no report for this date")
		return
	}
	writeReportJSON(w, date, content)
}

func writeReportJSON(w http.ResponseWriter, date time.Time, content string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"date":    date.Format("2006-01-02"),
		"content": content,
	})
}
