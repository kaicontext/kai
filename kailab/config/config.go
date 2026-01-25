// Package config provides configuration for the Kailab server.
package config

import (
	"os"
	"strconv"
	"strings"
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
	// SSHAllowUsers is a list of SSH usernames allowed to access git operations.
	SSHAllowUsers []string
	// SSHAllowRepos is a list of repos (tenant/repo) allowed for SSH git operations.
	SSHAllowRepos []string
	// SSHAudit enables audit logging for SSH git operations.
	SSHAudit bool
	// GitMirrorEnabled toggles Kai->Git mirroring for refs.
	GitMirrorEnabled bool
	// GitMirrorDir is the base directory for Git mirror repos.
	GitMirrorDir string
	// GitMirrorAllowRepos is an allowlist of tenant/repo entries to mirror.
	GitMirrorAllowRepos []string
	// GitMirrorRollback disables mirroring without changing config.
	GitMirrorRollback bool
	// KaiPrimary marks Kai refs as authoritative; Git write path is disabled.
	KaiPrimary bool
	// RequireSignedChangeSets enforces signatures for changeset writes.
	RequireSignedChangeSets bool
}

// FromEnv creates a Config from environment variables.
func FromEnv() *Config {
	cfg := &Config{
		Listen:                  getEnv("KAILAB_LISTEN", ":7447"),
		DataDir:                 getEnv("KAILAB_DATA", "./data"),
		Tenant:                  getEnv("KAILAB_TENANT", "default"),
		Repo:                    getEnv("KAILAB_REPO", "main"),
		MaxPackSize:             getEnvInt64("KAILAB_MAX_PACK_SIZE", 256*1024*1024), // 256MB default
		Version:                 getEnv("KAILAB_VERSION", "0.1.0"),
		Debug:                   getEnvBool("KAILAB_DEBUG", false),
		MaxOpenRepos:            getEnvInt("KAILAB_MAX_OPEN", 256),
		IdleTTL:                 getEnvDuration("KAILAB_IDLE_TTL", 10*time.Minute),
		SSHAllowUsers:           getEnvList("KAILAB_SSH_ALLOW_USERS"),
		SSHAllowRepos:           getEnvList("KAILAB_SSH_ALLOW_REPOS"),
		SSHAudit:                getEnvBool("KAILAB_SSH_AUDIT", false),
		GitMirrorEnabled:        getEnvBool("KAILAB_GIT_MIRROR_ENABLED", false),
		GitMirrorDir:            getEnv("KAILAB_GIT_MIRROR_DIR", ""),
		GitMirrorAllowRepos:     getEnvList("KAILAB_GIT_MIRROR_ALLOW_REPOS"),
		GitMirrorRollback:       getEnvBool("KAILAB_GIT_MIRROR_ROLLBACK", false),
		KaiPrimary:              getEnvBool("KAILAB_KAI_PRIMARY", false),
		RequireSignedChangeSets: getEnvBool("KAILAB_REQUIRE_SIGNED_CHANGESETS", false),
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

func getEnvList(key string) []string {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	return strings.FieldsFunc(val, func(r rune) bool {
		return r == ',' || r == ';'
	})
}
