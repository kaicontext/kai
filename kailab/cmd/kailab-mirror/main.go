// Command kailab-mirror syncs Kai refs into Git mirror repos.
package main

import (
	"context"
	"flag"
	"log"
	"path/filepath"
	"strings"

	"kailab/config"
	"kailab/repo"
	"kailab/sshserver"
)

func main() {
	dataDir := flag.String("data", "", "Data directory (default: ./data)")
	mirrorDir := flag.String("mirror-dir", "", "Git mirror base dir (default: <data>/git-mirror)")
	tenant := flag.String("tenant", "", "Tenant/org name")
	repoName := flag.String("repo", "", "Repository name")
	all := flag.Bool("all", false, "Sync all repos")
	allowRepos := flag.String("allow-repos", "", "Comma/semicolon-separated allowlist (tenant/repo)")
	rollback := flag.Bool("rollback", false, "Disable syncing (dry-run)")
	flag.Parse()

	cfg := config.FromEnv()
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if *mirrorDir != "" {
		cfg.GitMirrorDir = *mirrorDir
	}
	if cfg.GitMirrorDir == "" {
		cfg.GitMirrorDir = filepath.Join(cfg.DataDir, "git-mirror")
	}
	if *allowRepos != "" {
		cfg.GitMirrorAllowRepos = splitList(*allowRepos)
	}
	if *rollback {
		cfg.GitMirrorRollback = true
	}

	if !*all && (*tenant == "" || *repoName == "") {
		log.Fatal("provide --tenant and --repo (or use --all)")
	}

	registry := repo.NewRegistry(repo.RegistryConfig{
		DataDir: cfg.DataDir,
		MaxOpen: 64,
	})
	defer registry.Close()

	mirror := sshserver.NewGitMirror(sshserver.MirrorConfig{
		Enabled:    true,
		BaseDir:    cfg.GitMirrorDir,
		AllowRepos: cfg.GitMirrorAllowRepos,
		Rollback:   cfg.GitMirrorRollback,
		Logger:     log.Default(),
	})

	ctx := context.Background()
	if *all {
		if err := syncAll(ctx, registry, mirror); err != nil {
			log.Fatal(err)
		}
		return
	}

	handle, err := registry.Get(ctx, *tenant, *repoName)
	if err != nil {
		log.Fatal(err)
	}
	registry.Acquire(handle)
	defer registry.Release(handle)

	log.Printf("syncing mirror refs for %s/%s", handle.Tenant, handle.Name)
	if err := mirror.SyncAllRefs(ctx, handle); err != nil {
		log.Fatal(err)
	}
	log.Printf("mirror sync complete for %s/%s", handle.Tenant, handle.Name)
}

func syncAll(ctx context.Context, registry *repo.Registry, mirror *sshserver.GitMirror) error {
	tenants, err := registry.ListTenants(ctx)
	if err != nil {
		return err
	}
	for _, tenant := range tenants {
		repos, err := registry.List(ctx, tenant)
		if err != nil {
			return err
		}
		for _, name := range repos {
			handle, err := registry.Get(ctx, tenant, name)
			if err != nil {
				return err
			}
			registry.Acquire(handle)
			log.Printf("syncing mirror refs for %s/%s", handle.Tenant, handle.Name)
			err = mirror.SyncAllRefs(ctx, handle)
			registry.Release(handle)
			if err != nil {
				return err
			}
			log.Printf("mirror sync complete for %s/%s", handle.Tenant, handle.Name)
		}
	}
	return nil
}

func splitList(val string) []string {
	if val == "" {
		return nil
	}
	parts := strings.FieldsFunc(val, func(r rune) bool {
		return r == ',' || r == ';'
	})
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
