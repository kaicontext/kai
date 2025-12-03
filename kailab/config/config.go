// Package config provides configuration for the Kailab server.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds server configuration.
type Config struct {
	// Listen is the address to listen on (e.g., ":7447").
	Listen string
	// DataDir is the root directory for database files.
	DataDir string
	// Tenant is the tenant/org identifier (legacy, for single-repo mode).
	Tenant string
	// Repo is the repository name (legacy, for single-repo mode).
	Repo string
	// MaxPackSize is the maximum allowed pack size in bytes.
	MaxPackSize int64
	// Version is the server version string.
	Version string
	// Debug enables debug logging.
	Debug bool
	// MaxOpenRepos is the maximum number of repos to keep open (LRU cache size).
	MaxOpenRepos int
	// IdleTTL is how long to keep idle repos open before closing.
	IdleTTL time.Duration
}

// FromEnv creates a Config from environment variables.
func FromEnv() *Config {
	cfg := &Config{
		Listen:       getEnv("KAILAB_LISTEN", ":7447"),
		DataDir:      getEnv("KAILAB_DATA", "./data"),
		Tenant:       getEnv("KAILAB_TENANT", "default"),
		Repo:         getEnv("KAILAB_REPO", "main"),
		MaxPackSize:  getEnvInt64("KAILAB_MAX_PACK_SIZE", 256*1024*1024), // 256MB default
		Version:      getEnv("KAILAB_VERSION", "0.1.0"),
		Debug:        getEnvBool("KAILAB_DEBUG", false),
		MaxOpenRepos: getEnvInt("KAILAB_MAX_OPEN", 256),
		IdleTTL:      getEnvDuration("KAILAB_IDLE_TTL", 10*time.Minute),
	}
	return cfg
}

// FromArgs creates a Config from explicit values, with env fallbacks.
func FromArgs(listen, dataDir, tenant, repo string) *Config {
	cfg := FromEnv()
	if listen != "" {
		cfg.Listen = listen
	}
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	if tenant != "" {
		cfg.Tenant = tenant
	}
	if repo != "" {
		cfg.Repo = repo
	}
	return cfg
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt64(key string, defaultVal int64) int64 {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
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

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
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
