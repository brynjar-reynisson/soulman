package claudesession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// userHomeDir is a variable so tests can point it at a temp directory.
var userHomeDir = os.UserHomeDir

// homeProfileEnv returns the current environment minus CLAUDE_CONFIG_DIR, so
// the spawned session always uses the default home profile (~/.claude and
// ~/.claude.json) instead of whichever profile (e.g. ~/.claude-work) the
// service happened to inherit from the shell that started it.
func homeProfileEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "CLAUDE_CONFIG_DIR=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// trustDirectory marks dir as trusted in the home profile's ~/.claude.json
// (projects[<dir>].hasTrustDialogAccepted = true), so a background session
// started there doesn't stall on the workspace-trust dialog. It is a no-op
// when the directory is already trusted. The file is rewritten atomically
// (temp file + rename).
func trustDirectory(dir string) error {
	home, err := userHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		cfg["projects"] = projects
	}
	key := filepath.ToSlash(dir)
	entry, _ := projects[key].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		projects[key] = entry
	}
	if accepted, _ := entry["hasTrustDialogAccepted"].(bool); accepted {
		return nil
	}
	entry["hasTrustDialogAccepted"] = true
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".soulman.tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
