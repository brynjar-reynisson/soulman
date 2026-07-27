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

func TestParseLine_NonErrorLevel_ReturnsFalse(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		level string
	}{
		{
			name:  "WARN level",
			line:  "2026/07/27 10:05:00 WARN something might be wrong",
			level: "WARN",
		},
		{
			name:  "INFO level",
			line:  "2026/07/27 10:05:00 INFO service started",
			level: "INFO",
		},
		{
			name:  "DEBUG level",
			line:  "2026/07/27 10:05:00 DEBUG internal state x=1 y=2",
			level: "DEBUG",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLine(tt.line)
			if ok {
				t.Errorf("parseLine(%q) ok = true, want false for %s level", tt.line, tt.level)
			}
			if got != (ParsedLine{}) {
				t.Errorf("parseLine(%q) returned %+v, want zero value", tt.line, got)
			}
		})
	}
}

func TestParseLine_MalformedLine_ReturnsFalse(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{
			name: "stack trace continuation",
			line: "    at github.com/example/main.go:42",
		},
		{
			name: "panic line",
			line: "panic: something went wrong",
		},
		{
			name: "missing level",
			line: "2026/07/27 10:05:00 this line has no level",
		},
		{
			name: "missing timestamp",
			line: "ERROR something broke",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLine(tt.line)
			if ok {
				t.Errorf("parseLine(%q) ok = true, want false for malformed line", tt.line)
			}
			if got != (ParsedLine{}) {
				t.Errorf("parseLine(%q) returned %+v, want zero value", tt.line, got)
			}
		})
	}
}

func TestParseLine_NoAttributes_ReturnsFullMsg(t *testing.T) {
	line := `2026/07/27 10:05:00 ERROR database connection timeout after 30 seconds`

	got, ok := parseLine(line)
	if !ok {
		t.Fatalf("parseLine(%q) ok = false, want true", line)
	}
	if got.Level != "ERROR" {
		t.Errorf("Level = %q, want ERROR", got.Level)
	}
	if got.Msg != "database connection timeout after 30 seconds" {
		t.Errorf("Msg = %q, want %q", got.Msg, "database connection timeout after 30 seconds")
	}
}

func TestParseLine_EmptyMessage_ReturnsEmptyMsg(t *testing.T) {
	line := `2026/07/27 10:05:00 ERROR retry_count=5 backoff_ms=1000`

	got, ok := parseLine(line)
	if !ok {
		t.Fatalf("parseLine(%q) ok = false, want true", line)
	}
	if got.Level != "ERROR" {
		t.Errorf("Level = %q, want ERROR", got.Level)
	}
	if got.Msg != "" {
		t.Errorf("Msg = %q, want empty string", got.Msg)
	}
}
