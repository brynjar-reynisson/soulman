// Package config reads projects-svc's runtime configuration from
// environment variables only — unlike web-svc/memory-svc, projects-svc has
// no cross-service settings to read from sharedconfig, so it doesn't call
// sharedconfig.Load at all.
package config

import "os"

type Config struct {
	DatabaseURL string
	Schema      string
	HTTPPort    string
	NotifyPort  string
}

func Load() *Config {
	return &Config{
		DatabaseURL: env("DATABASE_URL", "postgres://postgres:postgres@localhost:54322/postgres"),
		Schema:      env("SCHEMA", "projects_dev"),
		HTTPPort:    env("HTTP_PORT", "9006"),
		NotifyPort:  env("NOTIFY_PORT", "9007"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
