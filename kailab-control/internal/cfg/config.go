// Package cfg provides configuration for the Kailab control plane.
package cfg

import (
	"encoding/json"
	"os"
	"strconv"
	"time"
)

// Config holds control plane configuration.
type Config struct {
	// Listen is the address to listen on (e.g., ":8080").
	Listen string
	// DBURL is the database URL (SQLite path or Postgres URL).
	DBURL string
	// JWTSigningKey is the key used to sign JWTs.
	JWTSigningKey []byte
	// JWTIssuer is the JWT issuer claim.
	JWTIssuer string
	// AccessTokenTTL is how long access tokens are valid.
	AccessTokenTTL time.Duration
	// RefreshTokenTTL is how long refresh tokens are valid.
	RefreshTokenTTL time.Duration
	// MagicLinkTTL is how long magic links are valid.
	MagicLinkTTL time.Duration
	// MagicLinkFrom is the email sender for magic links.
	MagicLinkFrom string
	// PostmarkToken is the Postmark server token for sending emails.
	PostmarkToken string
	// BaseURL is the public base URL for this service.
	BaseURL string
	// Shards is a map of shard name to URL.
	Shards map[string]string
	// Debug enables debug logging.
	Debug bool
	// Version is the server version string.
	Version string
}

// FromEnv creates a Config from environment variables.
func FromEnv() *Config {
	cfg := &Config{
		Listen:          getEnv("KLC_LISTEN", ":8080"),
		DBURL:           getEnv("KLC_DB_URL", "kailab-control.db"),
		JWTSigningKey:   []byte(getEnv("KLC_JWT_SIGNING_KEY", "dev-secret-key-change-in-production")),
		JWTIssuer:       getEnv("KLC_JWT_ISSUER", "kailab-control"),
		AccessTokenTTL:  getEnvDuration("KLC_ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL: getEnvDuration("KLC_REFRESH_TOKEN_TTL", 7*24*time.Hour),
		MagicLinkTTL:    getEnvDuration("KLC_MAGIC_LINK_TTL", 15*time.Minute),
		MagicLinkFrom:   getEnv("KLC_MAGICLINK_FROM", "noreply@1medium.ai"),
		PostmarkToken:   getEnv("KLC_POSTMARK_TOKEN", ""),
		BaseURL:         getEnv("KLC_BASE_URL", "http://localhost:8080"),
		Debug:           getEnvBool("KLC_DEBUG", false),
		Version:         getEnv("KLC_VERSION", "0.1.0"),
	}

	// Parse shards from JSON
	shardsJSON := getEnv("KLC_SHARDS_JSON", `{"default":"http://localhost:7447"}`)
	if err := json.Unmarshal([]byte(shardsJSON), &cfg.Shards); err != nil {
		cfg.Shards = map[string]string{"default": "http://localhost:7447"}
	}

	return cfg
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			return b
		}
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}
