package config_test

import (
	"os"
	"testing"

	"soulman/projects-svc/config"
)

func TestLoad_DefaultsWhenEnvUnset(t *testing.T) {
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("SCHEMA")
	os.Unsetenv("HTTP_PORT")
	os.Unsetenv("NOTIFY_PORT")

	cfg := config.Load()

	if cfg.DatabaseURL != "postgres://postgres:postgres@localhost:54322/postgres" {
		t.Errorf("DatabaseURL = %q, want the default local Postgres URL", cfg.DatabaseURL)
	}
	if cfg.Schema != "projects_dev" {
		t.Errorf("Schema = %q, want projects_dev", cfg.Schema)
	}
	if cfg.HTTPPort != "9006" {
		t.Errorf("HTTPPort = %q, want 9006", cfg.HTTPPort)
	}
	if cfg.NotifyPort != "9007" {
		t.Errorf("NotifyPort = %q, want 9007", cfg.NotifyPort)
	}
}

func TestLoad_ReadsFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("SCHEMA", "projects_prod")
	t.Setenv("HTTP_PORT", "9106")
	t.Setenv("NOTIFY_PORT", "9107")

	cfg := config.Load()

	if cfg.DatabaseURL != "postgres://x/y" {
		t.Errorf("DatabaseURL = %q, want postgres://x/y", cfg.DatabaseURL)
	}
	if cfg.Schema != "projects_prod" {
		t.Errorf("Schema = %q, want projects_prod", cfg.Schema)
	}
	if cfg.HTTPPort != "9106" {
		t.Errorf("HTTPPort = %q, want 9106", cfg.HTTPPort)
	}
	if cfg.NotifyPort != "9107" {
		t.Errorf("NotifyPort = %q, want 9107", cfg.NotifyPort)
	}
}
