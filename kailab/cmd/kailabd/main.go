// Command kailabd is the Kailab server daemon.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kailab/api"
	"kailab/config"
	"kailab/repo"
)

func main() {
	// Parse flags
	listen := flag.String("listen", "", "Address to listen on (default: :7447)")
	dataDir := flag.String("data", "", "Data directory (default: ./data)")
	flag.Parse()

	// Load config (flags override env)
	cfg := config.FromEnv()
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}

	log.Printf("kailabd starting...")
	log.Printf("  listen:       %s", cfg.Listen)
	log.Printf("  data:         %s", cfg.DataDir)
	log.Printf("  max_open:     %d", cfg.MaxOpenRepos)
	log.Printf("  idle_ttl:     %s", cfg.IdleTTL)
	log.Printf("  max_pack:     %d MB", cfg.MaxPackSize/(1024*1024))
	log.Printf("  version:      %s", cfg.Version)

	// Create data directory if needed
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("failed to create data directory: %v", err)
	}

	// Create repo registry
	registry := repo.NewRegistry(repo.RegistryConfig{
		DataDir: cfg.DataDir,
		MaxOpen: cfg.MaxOpenRepos,
		IdleTTL: cfg.IdleTTL,
	})
	defer registry.Close()

	// Create HTTP server
	mux := api.NewRouter(registry, cfg)
	handler := api.WithDefaults(mux)

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Handle graceful shutdown
	done := make(chan struct{})
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
		<-sigint

		log.Println("shutting down...")

		// Give connections 30s to finish
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}

		close(done)
	}()

	// Start server
	log.Printf("kailabd listening on %s", cfg.Listen)
	log.Printf("Multi-repo mode: routes are /{tenant}/{repo}/v1/...")
	log.Printf("Admin routes: POST /admin/v1/repos, GET /admin/v1/repos, DELETE /admin/v1/repos/{tenant}/{repo}")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}

	<-done
	log.Println("kailabd stopped")
}
