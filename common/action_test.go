package common_test

import (
	"encoding/json"
	"testing"

	"soulman/common"
)

func TestActionRequest_JSONRoundtrip(t *testing.T) {
	req := common.ActionRequest{
		CorrelationID:   "018f1a2b-corr",
		Intent:          "Log this error to today's daily report",
		ActionHint:      "append_daily_report_entry",
		Parameters:      json.RawMessage(`{"summary":"boom"}`),
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "retry once, then give up silently",
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got common.ActionRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CorrelationID != req.CorrelationID || got.ActionHint != req.ActionHint {
		t.Errorf("got = %+v, want %+v", got, req)
	}
}

// TestActionRequest_WireFieldNames guards against the exact bug this package
// was created to prevent: thinking-svc and action-svc independently defining
// incompatible field names for the same message (correlation_id/action_hint
// vs task_id/action_type). Since both services now import this one type,
// this test only needs to run once — but it documents the wire contract
// explicitly so a future field rename doesn't silently reintroduce drift.
func TestActionRequest_WireFieldNames(t *testing.T) {
	req := common.ActionRequest{CorrelationID: "c1", ActionHint: "append_daily_report_entry"}
	b, _ := json.Marshal(req)

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, ok := m["correlation_id"]; !ok {
		t.Error(`expected "correlation_id" field in wire JSON`)
	}
	if _, ok := m["action_hint"]; !ok {
		t.Error(`expected "action_hint" field in wire JSON`)
	}
	if _, ok := m["task_id"]; ok {
		t.Error(`"task_id" should not appear - use correlation_id`)
	}
	if _, ok := m["action_type"]; ok {
		t.Error(`"action_type" should not appear - use action_hint`)
	}
}
