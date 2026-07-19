package common_test

import (
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
)

func TestOutcomeRecord_JSONRoundtrip(t *testing.T) {
	occurredAt := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	rec := common.OutcomeRecord{
		Type:       "action_log",
		ActionType: "triage_gmail_email",
		Status:     "success",
		TaskID:     "t1",
		OccurredAt: occurredAt,
		Summary:    "Server down — important",
		Decision:   "notified via Discord",
		Tags:       []string{"gmail", "triage"},
	}

	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got common.OutcomeRecord
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ActionType != rec.ActionType || got.Summary != rec.Summary || !got.OccurredAt.Equal(rec.OccurredAt) {
		t.Errorf("got = %+v, want %+v", got, rec)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "gmail" || got.Tags[1] != "triage" {
		t.Errorf("Tags = %v, want [gmail triage]", got.Tags)
	}
}

// TestOutcomeRecord_WireFieldNames documents the exact wire contract, the
// same way TestActionRequest_WireFieldNames does for ActionRequest — so a
// future field rename doesn't silently break the action-svc <-> memory-svc
// contract without a test catching it.
func TestOutcomeRecord_WireFieldNames(t *testing.T) {
	rec := common.OutcomeRecord{ActionType: "x", Status: "success", TaskID: "t1"}
	b, _ := json.Marshal(rec)

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, field := range []string{"type", "action_type", "status", "task_id", "occurred_at", "summary", "decision", "tags"} {
		if _, ok := m[field]; !ok {
			t.Errorf("expected %q field in wire JSON", field)
		}
	}
}
