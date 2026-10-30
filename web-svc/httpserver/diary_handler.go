package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"soulman/web-svc/diary"
)

func writeDiaryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, diary.ErrRootUnavailable):
		writeJSONError(w, http.StatusServiceUnavailable, "diary unavailable")
	case errors.Is(err, diary.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, diary.ErrInvalidName):
		writeJSONError(w, http.StatusBadRequest, "invalid name")
	case errors.Is(err, diary.ErrEmptyContent):
		writeJSONError(w, http.StatusBadRequest, "content is empty")
	case errors.Is(err, diary.ErrExists):
		writeJSONError(w, http.StatusConflict, "already exists")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *Server) diaryTree(w http.ResponseWriter, r *http.Request) {
	years, err := diary.Tree(s.cfg.DiaryRoot)
	if err != nil {
		writeDiaryError(w, err)
		return
	}
	if years == nil {
		years = []diary.Year{}
	}
	writeJSON(w, http.StatusOK, map[string][]diary.Year{"years": years})
}

func (s *Server) diaryEntriesGet(w http.ResponseWriter, r *http.Request) {
	entries, err := diary.ListDay(s.cfg.DiaryRoot, r.URL.Query().Get("date"))
	if err != nil {
		writeDiaryError(w, err)
		return
	}
	if entries == nil {
		entries = []diary.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string][]diary.Entry{"entries": entries})
}

type diaryEntryRequest struct {
	Date    string `json:"date"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (s *Server) diaryEntriesPost(w http.ResponseWriter, r *http.Request) {
	var req diaryEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	date, e, err := diary.Create(s.cfg.DiaryRoot, req.Content, time.Now())
	if err != nil {
		writeDiaryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"date": date, "name": e.Name, "time": e.Time})
}

func (s *Server) diaryEntriesPut(w http.ResponseWriter, r *http.Request) {
	var req diaryEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := diary.Update(s.cfg.DiaryRoot, req.Date, req.Name, req.Content); err != nil {
		writeDiaryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
