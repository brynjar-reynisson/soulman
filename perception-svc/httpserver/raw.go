package httpserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"soulman/common"
)

// perceiveRaw implements docs/superpowers/specs/2026-07-18-pipeline-debugging-tools-design.md's
// generic Stimulus injection endpoint: the request body may be a complete
// common.Stimulus or just the essentials — any required field left blank
// gets a sensible default filled in, matching what buildCLIStimulus already
// does for the CLI channel: stimulus_id, schema_version, received_at, and
// occurred_at (defaulted to received_at, since rule handlers pass it
// straight into a time.Parse downstream — leaving it nil would silently
// no-op dispatch rather than fail loudly). channel has no default: it's the
// one thing the caller must always specify, since "which channel is this
// pretending to be" can't be inferred.
func (s *Server) perceiveRaw(w http.ResponseWriter, r *http.Request) {
	var stimulus common.Stimulus
	if err := json.NewDecoder(r.Body).Decode(&stimulus); err != nil {
		writeCLIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if stimulus.Channel == "" {
		writeCLIError(w, http.StatusBadRequest, "channel is required")
		return
	}

	if stimulus.StimulusID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			id = uuid.New()
		}
		stimulus.StimulusID = id.String()
	}
	if stimulus.SchemaVersion == 0 {
		stimulus.SchemaVersion = 1
	}
	if stimulus.ReceivedAt.IsZero() {
		stimulus.ReceivedAt = time.Now().UTC()
	}
	if stimulus.OccurredAt == nil {
		stimulus.OccurredAt = &stimulus.ReceivedAt
	}

	if err := s.publisher.Publish(r.Context(), &stimulus); err != nil {
		writeCLIError(w, http.StatusServiceUnavailable, "failed to publish stimulus")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"stimulus_id": stimulus.StimulusID})
}
