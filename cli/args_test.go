package main

import "testing"

func TestParseArgs_PlainText_DefaultsToStimulusMode(t *testing.T) {
	got, err := parseArgs([]string{"remind me to check logs"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "stimulus" {
		t.Errorf("Mode = %q, want stimulus", got.Mode)
	}
	if got.Text != "remind me to check logs" {
		t.Errorf("Text = %q, want the input text", got.Text)
	}
	if got.Priority != "normal" {
		t.Errorf("Priority = %q, want normal (default)", got.Priority)
	}
	if got.Dev {
		t.Error("Dev = true, want false (default)")
	}
}

func TestParseArgs_NoteSubcommand_SetsNoteMode(t *testing.T) {
	got, err := parseArgs([]string{"note", "disk cleanup done"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "note" {
		t.Errorf("Mode = %q, want note", got.Mode)
	}
	if got.Text != "disk cleanup done" {
		t.Errorf("Text = %q, want the input text", got.Text)
	}
}

func TestParseArgs_PriorityFlag(t *testing.T) {
	got, err := parseArgs([]string{"--priority", "high", "note", "server is on fire"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Priority != "high" {
		t.Errorf("Priority = %q, want high", got.Priority)
	}
	if got.Mode != "note" || got.Text != "server is on fire" {
		t.Errorf("Mode/Text = %q/%q, want note/'server is on fire'", got.Mode, got.Text)
	}
}

func TestParseArgs_DevFlag(t *testing.T) {
	got, err := parseArgs([]string{"--dev", "hello"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !got.Dev {
		t.Error("Dev = false, want true")
	}
}

func TestParseArgs_InvalidPriority_Errors(t *testing.T) {
	_, err := parseArgs([]string{"--priority", "urgent", "hello"})
	if err == nil {
		t.Fatal("expected an error for an invalid --priority value")
	}
}

func TestParseArgs_NoText_Errors(t *testing.T) {
	_, err := parseArgs([]string{})
	if err == nil {
		t.Fatal("expected an error when no text is given")
	}
}

func TestParseArgs_NoteWithNoText_Errors(t *testing.T) {
	_, err := parseArgs([]string{"note"})
	if err == nil {
		t.Fatal("expected an error when 'note' has no following text")
	}
}

func TestParseArgs_MultiWordText_JoinsWithSpace(t *testing.T) {
	got, err := parseArgs([]string{"remind", "me", "to", "check", "logs"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Text != "remind me to check logs" {
		t.Errorf("Text = %q, want joined words", got.Text)
	}
}

func TestParseArgs_EmptyTextArgument_Errors(t *testing.T) {
	_, err := parseArgs([]string{""})
	if err == nil {
		t.Fatal("expected an error for an empty text argument")
	}
}

func TestParseArgs_WhitespaceOnlyTextArgument_Errors(t *testing.T) {
	_, err := parseArgs([]string{"note", "   "})
	if err == nil {
		t.Fatal("expected an error for a whitespace-only text argument")
	}
}

func TestParseArgs_UnrecognizedFlag_Errors(t *testing.T) {
	_, err := parseArgs([]string{"--priorty", "high", "hello"})
	if err == nil {
		t.Fatal("expected an error for an unrecognized --flag-looking token")
	}
}

func TestParseArgs_EndOfFlagsSeparator_AllowsDashDashText(t *testing.T) {
	got, err := parseArgs([]string{"note", "--", "--downtime", "was", "caused", "by", "a", "bad", "deploy"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "note" {
		t.Errorf("Mode = %q, want note", got.Mode)
	}
	if got.Text != "--downtime was caused by a bad deploy" {
		t.Errorf("Text = %q, want %q", got.Text, "--downtime was caused by a bad deploy")
	}
}

func TestParseArgs_InjectMode(t *testing.T) {
	got, err := parseArgs([]string{"inject", "path/to/file.json"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "inject" {
		t.Errorf("Mode = %q, want inject", got.Mode)
	}
	if got.InjectFile != "path/to/file.json" {
		t.Errorf("InjectFile = %q, want path/to/file.json", got.InjectFile)
	}
}

func TestParseArgs_InjectMode_WithDevFlag(t *testing.T) {
	got, err := parseArgs([]string{"--dev", "inject", "path/to/file.json"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "inject" || got.InjectFile != "path/to/file.json" || !got.Dev {
		t.Errorf("got %+v, want Mode=inject InjectFile=path/to/file.json Dev=true", got)
	}
}

func TestParseArgs_InjectMode_MissingFile_ReturnsError(t *testing.T) {
	_, err := parseArgs([]string{"inject"})
	if err == nil {
		t.Fatal("parseArgs: want error for inject with no file argument, got nil")
	}
}

func TestParseArgs_DiscordHistoryMode(t *testing.T) {
	got, err := parseArgs([]string{"discord-history"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "discord-history" {
		t.Errorf("Mode = %q, want discord-history", got.Mode)
	}
	if got.DiscordHistoryLimit != 20 {
		t.Errorf("DiscordHistoryLimit = %d, want default 20", got.DiscordHistoryLimit)
	}
}

func TestParseArgs_DiscordHistoryMode_WithLimit(t *testing.T) {
	got, err := parseArgs([]string{"discord-history", "--limit", "50"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.DiscordHistoryLimit != 50 {
		t.Errorf("DiscordHistoryLimit = %d, want 50", got.DiscordHistoryLimit)
	}
}

func TestParseArgs_DiscordHistoryMode_InvalidLimit_ReturnsError(t *testing.T) {
	_, err := parseArgs([]string{"discord-history", "--limit", "not-a-number"})
	if err == nil {
		t.Fatal("parseArgs: want error for non-numeric --limit, got nil")
	}
}
