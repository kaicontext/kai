// Command kailab-control is the Kailab control plane server.
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

	"kailab-control/internal/api"
	"kailab-control/internal/auth"
	"kailab-control/internal/cfg"
	"kailab-control/internal/db"
	"kailab-control/internal/routing"
)

func main() {
	// Parse flags
	listen := flag.String("listen", "", "Address to listen on (default: :8080)")
	dbURL := flag.String("db", "", "Database URL (default: kailab-control.db)")
	flag.Parse()

	// Load config
	config := cfg.FromEnv()
	if *listen != "" {
		config.Listen = *listen
	}
	if *dbURL != "" {
		config.DBURL = *dbURL
	}

	log.Printf("kailab-control starting...")
	log.Printf("  listen:     %s", config.Listen)
	log.Printf("  db:         %s", config.DBURL)
	log.Printf("  base_url:   %s", config.BaseURL)
	log.Printf("  version:    %s", config.Version)
	log.Printf("  debug:      %v", config.Debug)
	log.Printf("  shards:     %v", config.Shards)

	// Open database
	database, err := db.Open(config.DBURL)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	log.Printf("Database opened successfully")

	// Create token service
	tokens := auth.NewTokenService(
		config.JWTSigningKey,
		config.JWTIssuer,
		config.AccessTokenTTL,
		config.RefreshTokenTTL,
	)

	// Create shard picker
	shards := routing.NewShardPicker(config.Shards)

	// Create handler
	handler := api.NewHandler(database, config, tokens, shards)

	// Create router
	router := api.NewRouter(handler)
	wrappedHandler := api.WithDefaults(router, config.Debug)

	// Create HTTP server
	srv := &http.Server{
		Addr:         config.Listen,
		Handler:      wrappedHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // Long for proxied uploads
		IdleTimeout:  120 * time.Second,
	}

	// Handle graceful shutdown
	done := make(chan struct{})
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
		<-sigint

		log.Println("Shutting down...")

		// Give connections 30s to finish
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Shutdown error: %v", err)
		}

		close(done)
	}()

	// Start cleanup goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := database.CleanupExpiredMagicLinks(); err != nil {
					log.Printf("Failed to cleanup magic links: %v", err)
				}
			case <-done:
				return
			}
		}
	}()

	// Start server
	log.Printf("kailab-control listening on %s", config.Listen)
	log.Printf("API routes:")
	log.Printf("  POST /v1/auth/magic-link  - Request login link")
	log.Printf("  POST /v1/auth/token       - Exchange magic token for JWT")
	log.Printf("  GET  /v1/me               - Get current user")
	log.Printf("  POST /v1/orgs             - Create org")
	log.Printf("  POST /v1/orgs/:org/repos  - Create repo")
	log.Printf("  ANY  /:org/:repo/v1/*     - Proxy to data plane")

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}

	<-done
	log.Println("kailab-control stopped")
}
