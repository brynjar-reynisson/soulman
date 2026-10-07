package claudesession

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomeProfileEnvDropsConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/home/x/.claude-work")
	for _, kv := range homeProfileEnv() {
		if strings.HasPrefix(strings.ToUpper(kv), "CLAUDE_CONFIG_DIR=") {
			t.Fatalf("CLAUDE_CONFIG_DIR leaked: %s", kv)
		}
	}
}

func TestTrustDirectory(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = os.UserHomeDir })
	cfgPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(cfgPath, []byte(`{"numStartups": 253, "projects": {"C:/a": {"hasTrustDialogAccepted": true, "x": 1}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trustDirectory(`C:\b`); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfgPath)
	s := string(got)
	for _, want := range []string{`"C:/b"`, `"numStartups": 253`, `"C:/a"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if strings.Count(s, "hasTrustDialogAccepted\": true") != 2 {
		t.Errorf("expected two trusted entries: %s", s)
	}
}
