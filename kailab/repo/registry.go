// Package repo provides multi-repo management with LRU caching.
package repo

import (
	"container/list"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "embed"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

//go:embed pragmas.sql
var pragmasSQL string

var (
	ErrRepoNotFound = errors.New("repo not found")
	ErrRepoExists   = errors.New("repo already exists")
)

// Handle represents an open repository with its database and services.
type Handle struct {
	Tenant    string
	Name      string
	Path      string
	DB        *sql.DB
	lastUsed  time.Time
	active    int32 // number of active requests
	mu        sync.Mutex
	element   *list.Element // position in LRU list
	enrichJob chan []byte   // channel to send enrichment jobs
	enrichQuit chan struct{}
}

// RegistryConfig configures the repo registry.
type RegistryConfig struct {
	DataDir string        // Base directory for all repos
	MaxOpen int           // Maximum number of open repos (LRU capacity)
	IdleTTL time.Duration // Close repos idle longer than this
}

// Registry manages multiple repositories with LRU caching.
type Registry struct {
	cfg     RegistryConfig
	mu      sync.RWMutex
	repos   map[string]*Handle // key: "tenant/repo"
	lru     *list.List         // LRU list of repo keys
	stop    chan struct{}
}

// NewRegistry creates a new repo registry.
func NewRegistry(cfg RegistryConfig) *Registry {
	if cfg.MaxOpen <= 0 {
		cfg.MaxOpen = 256
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 10 * time.Minute
	}

	r := &Registry{
		cfg:   cfg,
		repos: make(map[string]*Handle),
		lru:   list.New(),
		stop:  make(chan struct{}),
	}

	// Start idle reaper
	go r.reapLoop()

	return r
}

// Get returns a handle to the specified repo, opening it if needed.
func (r *Registry) Get(ctx context.Context, tenant, repo string) (*Handle, error) {
	key := tenant + "/" + repo

	// Fast path: check if already open
	r.mu.RLock()
	h, ok := r.repos[key]
	r.mu.RUnlock()

	if ok {
		r.touch(h)
		return h, nil
	}

	// Slow path: need to open
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if h, ok := r.repos[key]; ok {
		r.touchLocked(h)
		return h, nil
	}

	// Check if repo exists on disk
	repoPath := filepath.Join(r.cfg.DataDir, tenant, repo)
	dbPath := filepath.Join(repoPath, "repo.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, ErrRepoNotFound
	}

	// Open the repo
	h, err := r.openRepoLocked(tenant, repo, repoPath)
	if err != nil {
		return nil, err
	}

	return h, nil
}

// Create creates a new repository.
func (r *Registry) Create(ctx context.Context, tenant, repo string) (*Handle, error) {
	key := tenant + "/" + repo
	repoPath := filepath.Join(r.cfg.DataDir, tenant, repo)
	dbPath := filepath.Join(repoPath, "repo.db")

	// Check if already exists
	if _, err := os.Stat(dbPath); err == nil {
		return nil, ErrRepoExists
	}

	// Create directory structure
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return nil, fmt.Errorf("creating repo directory: %w", err)
	}

	// Create segments directory
	segDir := filepath.Join(repoPath, "segments")
	if err := os.MkdirAll(segDir, 0755); err != nil {
		return nil, fmt.Errorf("creating segments directory: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check
	if _, ok := r.repos[key]; ok {
		return nil, ErrRepoExists
	}

	// Open and initialize
	h, err := r.openRepoLocked(tenant, repo, repoPath)
	if err != nil {
		return nil, err
	}

	return h, nil
}

// Exists checks if a repo exists.
func (r *Registry) Exists(ctx context.Context, tenant, repo string) (bool, error) {
	repoPath := filepath.Join(r.cfg.DataDir, tenant, repo)
	dbPath := filepath.Join(repoPath, "repo.db")
	_, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// List returns all repos for a tenant.
func (r *Registry) List(ctx context.Context, tenant string) ([]string, error) {
	tenantPath := filepath.Join(r.cfg.DataDir, tenant)
	entries, err := os.ReadDir(tenantPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var repos []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dbPath := filepath.Join(tenantPath, e.Name(), "repo.db")
		if _, err := os.Stat(dbPath); err == nil {
			repos = append(repos, e.Name())
		}
	}
	return repos, nil
}

// ListTenants returns all tenants.
func (r *Registry) ListTenants(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(r.cfg.DataDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var tenants []string
	for _, e := range entries {
		if e.IsDir() {
			tenants = append(tenants, e.Name())
		}
	}
	return tenants, nil
}

// Delete soft-deletes a repo (renames directory).
func (r *Registry) Delete(ctx context.Context, tenant, repo string) error {
	key := tenant + "/" + repo

	r.mu.Lock()
	defer r.mu.Unlock()

	// Close if open
	if h, ok := r.repos[key]; ok {
		r.closeRepoLocked(h)
	}

	// Rename to .deleted suffix
	repoPath := filepath.Join(r.cfg.DataDir, tenant, repo)
	deletedPath := repoPath + ".deleted." + fmt.Sprintf("%d", time.Now().Unix())
	if err := os.Rename(repoPath, deletedPath); err != nil {
		return fmt.Errorf("deleting repo: %w", err)
	}

	return nil
}

// Acquire marks a handle as in-use (prevents eviction).
func (r *Registry) Acquire(h *Handle) {
	h.mu.Lock()
	h.active++
	h.lastUsed = time.Now()
	h.mu.Unlock()
}

// Release marks a handle as no longer in-use.
func (r *Registry) Release(h *Handle) {
	h.mu.Lock()
	h.active--
	h.lastUsed = time.Now()
	h.mu.Unlock()
}

// Close shuts down the registry.
func (r *Registry) Close() error {
	close(r.stop)

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, h := range r.repos {
		r.closeRepoLocked(h)
	}
	return nil
}

// EnqueueEnrichment sends a node ID to the repo's enrichment worker.
func (r *Registry) EnqueueEnrichment(h *Handle, nodeID []byte) {
	select {
	case h.enrichJob <- nodeID:
	default:
		// Queue full, skip
	}
}

// openRepoLocked opens a repo (must hold write lock).
func (r *Registry) openRepoLocked(tenant, repo, repoPath string) (*Handle, error) {
	key := tenant + "/" + repo

	// Evict if at capacity
	for len(r.repos) >= r.cfg.MaxOpen {
		if !r.evictOneLocked() {
			break // Can't evict any (all active)
		}
	}

	dbPath := filepath.Join(repoPath, "repo.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Apply pragmas
	if _, err := db.Exec(pragmasSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying pragmas: %w", err)
	}

	// Run schema
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	h := &Handle{
		Tenant:     tenant,
		Name:       repo,
		Path:       repoPath,
		DB:         db,
		lastUsed:   time.Now(),
		enrichJob:  make(chan []byte, 1024),
		enrichQuit: make(chan struct{}),
	}

	// Start enrichment worker
	go r.enrichLoop(h)

	// Add to LRU
	h.element = r.lru.PushFront(key)
	r.repos[key] = h

	return h, nil
}

// closeRepoLocked closes a repo (must hold write lock).
func (r *Registry) closeRepoLocked(h *Handle) {
	key := h.Tenant + "/" + h.Name

	// Stop enricher
	close(h.enrichQuit)

	// Close DB
	if h.DB != nil {
		h.DB.Close()
	}

	// Remove from LRU
	if h.element != nil {
		r.lru.Remove(h.element)
	}

	delete(r.repos, key)
}

// touch updates LRU position (acquires write lock).
func (r *Registry) touch(h *Handle) {
	r.mu.Lock()
	r.touchLocked(h)
	r.mu.Unlock()
}

// touchLocked updates LRU position (must hold write lock).
func (r *Registry) touchLocked(h *Handle) {
	h.lastUsed = time.Now()
	if h.element != nil {
		r.lru.MoveToFront(h.element)
	}
}

// evictOneLocked evicts the least recently used inactive repo.
func (r *Registry) evictOneLocked() bool {
	for e := r.lru.Back(); e != nil; e = e.Prev() {
		key := e.Value.(string)
		h := r.repos[key]
		h.mu.Lock()
		if h.active == 0 {
			h.mu.Unlock()
			r.closeRepoLocked(h)
			return true
		}
		h.mu.Unlock()
	}
	return false
}

// reapLoop periodically closes idle repos.
func (r *Registry) reapLoop() {
	ticker := time.NewTicker(r.cfg.IdleTTL / 2)
	defer ticker.Stop()

	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.reapIdle()
		}
	}
}

// reapIdle closes repos that have been idle too long.
func (r *Registry) reapIdle() {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-r.cfg.IdleTTL)

	for e := r.lru.Back(); e != nil; {
		key := e.Value.(string)
		h := r.repos[key]

		h.mu.Lock()
		idle := h.active == 0 && h.lastUsed.Before(cutoff)
		h.mu.Unlock()

		prev := e.Prev()
		if idle {
			r.closeRepoLocked(h)
		}
		e = prev
	}
}

// enrichLoop runs the background enrichment worker for a repo.
func (r *Registry) enrichLoop(h *Handle) {
	for {
		select {
		case <-h.enrichQuit:
			return
		case nodeID := <-h.enrichJob:
			r.processEnrichment(h, nodeID)
		}
	}
}

// processEnrichment processes a single enrichment job.
func (r *Registry) processEnrichment(h *Handle, nodeID []byte) {
	// TODO: Implement actual enrichment using kai-core
	// For now, just log
	// log.Printf("enriching node %x in %s/%s", nodeID[:8], h.Tenant, h.Name)
}
