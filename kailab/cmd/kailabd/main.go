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
	"kailab/background"
	"kailab/config"
	"kailab/store"
)

func main() {
	// Parse flags
	listen := flag.String("listen", "", "Address to listen on (default: :7447)")
	dataDir := flag.String("data", "", "Data directory (default: .kailab)")
	tenant := flag.String("tenant", "", "Tenant/org name (default: default)")
	repo := flag.String("repo", "", "Repository name (default: main)")
	flag.Parse()

	// Load config (flags override env)
	cfg := config.FromArgs(*listen, *dataDir, *tenant, *repo)

	log.Printf("kailabd starting...")
	log.Printf("  listen:  %s", cfg.Listen)
	log.Printf("  data:    %s", cfg.DataDir)
	log.Printf("  tenant:  %s", cfg.Tenant)
	log.Printf("  repo:    %s", cfg.Repo)
	log.Printf("  version: %s", cfg.Version)

	// Open database
	db, err := store.OpenRepoDB(cfg.DataDir, cfg.Tenant, cfg.Repo)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Start background enricher
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	enricher := background.NewEnricher(db)
	enricher.Start(ctx)
	defer enricher.Stop()

	// Create HTTP server
	mux := api.NewRouter(db, cfg)
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
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}

	<-done
	log.Println("kailabd stopped")
}
