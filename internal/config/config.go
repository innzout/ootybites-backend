// Package config loads and validates environment configuration once at boot.
// Secrets (Cloudinary/MSG91/DB/JWT) live here and never reach the client.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all runtime settings, loaded from the environment.
type Config struct {
	Env  string // "development" | "production"
	Port string

	DatabaseURL string
	RedisURL    string

	JWTSecret string

	// OTPDevMode skips MSG91 and accepts a logged/fixed OTP for local login.
	OTPDevMode bool

	// Admin bootstrap — an admin with these creds is upserted on boot.
	AdminUser string
	AdminPass string

	MSG91AuthKey    string
	MSG91TemplateID string
	MSG91SenderID   string

	CloudinaryCloudName string
	CloudinaryAPIKey    string
	CloudinaryAPISecret string

	CORSOrigins []string
}

// Load reads .env (if present) then the process environment, and fails fast
// when a required secret is missing.
func Load() (*Config, error) {
	// .env is optional; real deployments inject real env vars.
	_ = godotenv.Load()

	c := &Config{
		Env:                 get("APP_ENV", "development"),
		Port:                get("PORT", "8080"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		RedisURL:            os.Getenv("REDIS_URL"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		OTPDevMode:          get("OTP_DEV_MODE", "true") == "true",
		AdminUser:           get("ADMIN_USER", "admin"),
		AdminPass:           get("ADMIN_PASS", "admin123"),
		MSG91AuthKey:        os.Getenv("MSG91_AUTH_KEY"),
		MSG91TemplateID:     os.Getenv("MSG91_TEMPLATE_ID"),
		MSG91SenderID:       os.Getenv("MSG91_SENDER_ID"),
		CloudinaryCloudName: os.Getenv("CLOUDINARY_CLOUD_NAME"),
		CloudinaryAPIKey:    os.Getenv("CLOUDINARY_API_KEY"),
		CloudinaryAPISecret: os.Getenv("CLOUDINARY_API_SECRET"),
		CORSOrigins:         splitList(get("CORS_ORIGINS", "http://localhost:3000")),
	}

	// REDIS_URL is optional: when unset, the app falls back to an in-memory
	// rate limiter (mirrors hirzout-api's local behavior). DB and JWT are hard
	// requirements.
	var missing []string
	for k, v := range map[string]string{
		"DATABASE_URL": c.DatabaseURL,
		"JWT_SECRET":   c.JWTSecret,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	// A short JWT secret weakens every token. Require ≥32 chars.
	if len(c.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters (generate with: openssl rand -base64 48)")
	}
	return c, nil
}

// IsProd reports whether the app is running in production mode.
func (c *Config) IsProd() bool { return c.Env == "production" }

func get(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
