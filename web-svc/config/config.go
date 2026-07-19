package config

import (
	"fmt"
	"os"

	"soulman/common/sharedconfig"
)

type Config struct {
	HTTPPort          string
	SupabaseURL       string
	SupabaseJWTSecret string
	OwnerEmail        string
	CORSAllowedOrigin string
	PerceptionSvcURL  string
	MemorySvcURL      string
	ThinkingSvcURL    string
	ActionSvcURL      string
	SoulmanRoot       string
}

func Load() (*Config, error) {
	configPath := env("CONFIG_PATH", "./config.json")

	shared, err := sharedconfig.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading shared config: %w", err)
	}
	if shared.Web.OwnerEmail == "" {
		return nil, fmt.Errorf("shared config %s has no web.owner_email configured", configPath)
	}
	if shared.Web.CORSAllowedOrigin == "" {
		return nil, fmt.Errorf("shared config %s has no web.cors_allowed_origin configured", configPath)
	}
	if shared.Web.PerceptionSvcURL == "" {
		return nil, fmt.Errorf("shared config %s has no web.perception_svc_url configured", configPath)
	}
	if shared.Web.MemorySvcURL == "" {
		return nil, fmt.Errorf("shared config %s has no web.memory_svc_url configured", configPath)
	}
	if shared.Web.ThinkingSvcURL == "" {
		return nil, fmt.Errorf("shared config %s has no web.thinking_svc_url configured", configPath)
	}
	if shared.Web.ActionSvcURL == "" {
		return nil, fmt.Errorf("shared config %s has no web.action_svc_url configured", configPath)
	}

	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		return nil, fmt.Errorf("SUPABASE_URL environment variable is required")
	}
	// Optional: this Supabase project signs tokens with ES256 (verified via
	// its JWKS endpoint in web-svc/auth), which never touches this value.
	// Left blank, web-svc/auth.Verifier explicitly refuses to verify ANY
	// HS256 token at all (rather than attempting HMAC verification against
	// an empty key, which would be forgeable) — so an unset secret narrows
	// accepted tokens to ES256 only, it does not accept "anything."
	jwtSecret := env("SUPABASE_JWT_SECRET", "")

	return &Config{
		HTTPPort:          env("HTTP_PORT", "9005"),
		SupabaseURL:       supabaseURL,
		SupabaseJWTSecret: jwtSecret,
		OwnerEmail:        shared.Web.OwnerEmail,
		CORSAllowedOrigin: shared.Web.CORSAllowedOrigin,
		PerceptionSvcURL:  shared.Web.PerceptionSvcURL,
		MemorySvcURL:      shared.Web.MemorySvcURL,
		ThinkingSvcURL:    shared.Web.ThinkingSvcURL,
		ActionSvcURL:      shared.Web.ActionSvcURL,
		SoulmanRoot:       env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-dev`),
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
