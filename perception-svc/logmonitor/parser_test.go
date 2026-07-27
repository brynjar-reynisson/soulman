package logmonitor

import "testing"

func TestParseLine_ErrorLineWithAttrs_ExtractsLevelAndMsg(t *testing.T) {
	line := `2026/07/27 10:05:00 ERROR writer: DB insert failed, will replay on restart stimulus_id=abc123 error="dial tcp 127.0.0.1:5432: connect: connection refused"`

	got, ok := parseLine(line)
	if !ok {
		t.Fatalf("parseLine(%q) ok = false, want true", line)
	}
	if got.Level != "ERROR" {
		t.Errorf("Level = %q, want ERROR", got.Level)
	}
	if got.Msg != "writer: DB insert failed, will replay on restart" {
		t.Errorf("Msg = %q, want %q", got.Msg, "writer: DB insert failed, will replay on restart")
	}
}
