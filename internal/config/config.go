// Package config loads and validates environment configuration once at boot.
// Secrets (Cloudinary/MSG91/DB/JWT) live here and never reach the client.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// defaultAdminPass is the bootstrap admin password used only in development.
// Config.Load refuses to start in staging or production while it is still set.
const defaultAdminPass = "admin123"

// Config holds all runtime settings, loaded from the environment.
type Config struct {
	Env  string // "development" | "production"
	Port string
	// DBMaxConns caps the pgx pool. 0 = the db package default; see internal/db.New.
	DBMaxConns int

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
	// Optional Cloudinary upload preset. Must be a SIGNED preset — the name is
	// folded into the signature below, which an unsigned preset would reject.
	CloudinaryUploadPreset string

	CORSOrigins []string
}

// Load reads .env (if present) then the process environment, and fails fast
// when a required secret is missing.
func Load() (*Config, error) {
	// .env is optional; real deployments inject real env vars.
	_ = godotenv.Load()

	c := &Config{
		Env:                    get("APP_ENV", "development"),
		Port:                   get("PORT", "8080"),
		DBMaxConns:             getInt("DB_MAX_CONNS", 0),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		RedisURL:               os.Getenv("REDIS_URL"),
		JWTSecret:              os.Getenv("JWT_SECRET"),
		OTPDevMode:             get("OTP_DEV_MODE", "true") == "true",
		AdminUser:              get("ADMIN_USER", "admin"),
		AdminPass:              get("ADMIN_PASS", defaultAdminPass),
		MSG91AuthKey:           os.Getenv("MSG91_AUTH_KEY"),
		MSG91TemplateID:        os.Getenv("MSG91_TEMPLATE_ID"),
		MSG91SenderID:          os.Getenv("MSG91_SENDER_ID"),
		CloudinaryCloudName:    os.Getenv("CLOUDINARY_CLOUD_NAME"),
		CloudinaryAPIKey:       os.Getenv("CLOUDINARY_API_KEY"),
		CloudinaryAPISecret:    os.Getenv("CLOUDINARY_API_SECRET"),
		CloudinaryUploadPreset: os.Getenv("CLOUDINARY_UPLOAD_PRESET"),
		CORSOrigins:            splitList(get("CORS_ORIGINS", "http://localhost:3000")),
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
	// These two previously defaulted to the insecure value in every environment,
	// so forgetting one was silent: the admin panel stayed on a published
	// password, or the API kept handing out login codes in its own responses.
	//
	// Production refuses to boot - a crash on deploy is the loud failure these
	// need. Staging only warns, because staging deliberately runs dev OTP for
	// now and must stay deployable.
	if c.IsProd() {
		if c.AdminPass == defaultAdminPass {
			return nil, fmt.Errorf("ADMIN_PASS is still the default %q - set a real password", defaultAdminPass)
		}
		if c.OTPDevMode {
			return nil, fmt.Errorf("OTP_DEV_MODE must be false in production - dev mode returns the login OTP in the API response, letting anyone sign in as any phone number")
		}
	} else if !c.IsDev() {
		if c.AdminPass == defaultAdminPass {
			slog.Warn("ADMIN_PASS is the published default - anyone can reach the admin panel", "env", c.Env)
		}
		if c.OTPDevMode {
			slog.Warn("OTP_DEV_MODE is on - the API returns login OTPs in its response, so anyone can sign in as any phone number", "env", c.Env)
		}
	}
	return c, nil
}

// IsProd reports whether the app is running in production mode.
// APP_ENV is one of: development | staging | production.
//
// Only "development" relaxes anything. staging and production behave
// identically for security — strict CORS, no loopback exemption — because a
// staging box is reachable from the internet and should not be more permissive
// than the real thing. What differs between them is configuration (dev OTP,
// test data), not code.
func (c *Config) IsProd() bool { return c.Env == "production" }

// IsDev reports the one environment that gets relaxed behaviour.
func (c *Config) IsDev() bool { return c.Env == "development" }

// getInt reads an integer env var, returning fallback when unset or unparseable.
func getInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		slog.Warn("ignoring unparseable env var", "key", key, "value", v)
	}
	return fallback
}

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
		// Trailing slashes are stripped because a browser's Origin header never
		// has one: "https://app.vercel.app/" in CORS_ORIGINS would silently match
		// nothing, and the only symptom is every API call failing in the browser
		// while curl works fine.
		p = strings.TrimRight(strings.TrimSpace(p), "/")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
