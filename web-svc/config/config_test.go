package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"soulman/web-svc/config"
)

func writeConfigFile(t *testing.T, dir string, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}
	return path
}

const validConfigJSON = `{
  "web": {
    "owner_email": "breynisson@gmail.com",
    "cors_allowed_origin": "http://localhost:5178",
    "perception_svc_url": "http://localhost:9011",
    "memory_svc_url": "http://localhost:9012",
    "thinking_svc_url": "http://localhost:9013",
    "action_svc_url": "http://localhost:9014"
  }
}`

func TestLoad_DefaultsHTTPPortTo9005(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPPort != "9005" {
		t.Errorf("HTTPPort = %q, want 9005", cfg.HTTPPort)
	}
}

func TestLoad_HTTPPortEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("HTTP_PORT", "9015")
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("HTTP_PORT")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPPort != "9015" {
		t.Errorf("HTTPPort = %q, want 9015", cfg.HTTPPort)
	}
}

func TestLoad_MissingConfigFile_ReturnsError(t *testing.T) {
	os.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "does-not-exist.json"))
	defer os.Unsetenv("CONFIG_PATH")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error for a missing config file")
	}
}

func TestLoad_MissingSupabaseURL_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")
	os.Unsetenv("SUPABASE_URL")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error when SUPABASE_URL is unset")
	}
}

func TestLoad_MissingSupabaseJWTSecret_DefaultsToEmptyString(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want no error when SUPABASE_JWT_SECRET is unset", err)
	}
	if cfg.SupabaseJWTSecret != "" {
		t.Errorf("SupabaseJWTSecret = %q, want empty string", cfg.SupabaseJWTSecret)
	}
}

func TestLoad_PopulatesSupabaseAndOwnerFields(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SupabaseURL != "https://example.supabase.co" {
		t.Errorf("SupabaseURL = %q", cfg.SupabaseURL)
	}
	if cfg.SupabaseJWTSecret != "shh" {
		t.Errorf("SupabaseJWTSecret = %q", cfg.SupabaseJWTSecret)
	}
	if cfg.OwnerEmail != "breynisson@gmail.com" {
		t.Errorf("OwnerEmail = %q, want breynisson@gmail.com", cfg.OwnerEmail)
	}
}

func TestLoad_PopulatesDownstreamURLsAndCORSOrigin(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CORSAllowedOrigin != "http://localhost:5178" {
		t.Errorf("CORSAllowedOrigin = %q", cfg.CORSAllowedOrigin)
	}
	if cfg.PerceptionSvcURL != "http://localhost:9011" {
		t.Errorf("PerceptionSvcURL = %q", cfg.PerceptionSvcURL)
	}
	if cfg.MemorySvcURL != "http://localhost:9012" {
		t.Errorf("MemorySvcURL = %q", cfg.MemorySvcURL)
	}
	if cfg.ThinkingSvcURL != "http://localhost:9013" {
		t.Errorf("ThinkingSvcURL = %q", cfg.ThinkingSvcURL)
	}
	if cfg.ActionSvcURL != "http://localhost:9014" {
		t.Errorf("ActionSvcURL = %q", cfg.ActionSvcURL)
	}
}

func TestLoad_SoulmanRootDefaultsToDevPath(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")
	os.Unsetenv("SOULMAN_ROOT")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-dev` {
		t.Errorf("SoulmanRoot = %q", cfg.SoulmanRoot)
	}
}

func TestLoad_SoulmanRootEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	os.Setenv("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-prod`)
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")
	defer os.Unsetenv("SOULMAN_ROOT")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-prod` {
		t.Errorf("SoulmanRoot = %q", cfg.SoulmanRoot)
	}
}

func TestLoad_MissingDownstreamURL_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	incomplete := `{"web": {"owner_email": "breynisson@gmail.com", "cors_allowed_origin": "http://localhost:5178", "perception_svc_url": "http://localhost:9011", "memory_svc_url": "", "thinking_svc_url": "http://localhost:9013", "action_svc_url": "http://localhost:9014"}}`
	path := writeConfigFile(t, dir, incomplete)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want an error when a downstream URL is blank")
	}
}
