// Package background provides background processing for Kailab.
package background

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"kai-core/detect"
	"kai-core/intent"
	"kai-core/parse"
	"kailab/pack"
	"kailab/store"
)

// Enricher processes nodes that need symbol extraction, intent generation, etc.
type Enricher struct {
	db       *store.DB
	parser   *parse.Parser
	detector *detect.Detector
	interval time.Duration
	stop     chan struct{}
}

// NewEnricher creates a new background enricher.
func NewEnricher(db *store.DB) *Enricher {
	parser := parse.NewParser()
	return &Enricher{
		db:       db,
		parser:   parser,
		detector: detect.NewDetector(),
		interval: 1 * time.Second,
		stop:     make(chan struct{}),
	}
}

// Start begins the background processing loop.
func (e *Enricher) Start(ctx context.Context) {
	go e.run(ctx)
}

// Stop signals the enricher to stop.
func (e *Enricher) Stop() {
	close(e.stop)
}

func (e *Enricher) run(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stop:
			return
		case <-ticker.C:
			e.processOne()
		}
	}
}

func (e *Enricher) processOne() {
	item, err := e.db.ClaimEnrichmentItem()
	if err != nil {
		log.Printf("error claiming enrichment item: %v", err)
		return
	}
	if item == nil {
		return // No work to do
	}

	log.Printf("enriching %s node %x", item.Kind, item.NodeID[:8])

	errMsg := ""
	if err := e.enrich(item); err != nil {
		log.Printf("enrichment error for %x: %v", item.NodeID[:8], err)
		errMsg = err.Error()
	}

	if err := e.db.CompleteEnrichmentItem(item.ID, errMsg); err != nil {
		log.Printf("error completing enrichment item: %v", err)
	}
}

func (e *Enricher) enrich(item *store.EnrichQueueItem) error {
	switch item.Kind {
	case "Snapshot":
		return e.enrichSnapshot(item.NodeID)
	case "ChangeSet":
		return e.enrichChangeSet(item.NodeID)
	default:
		// Unknown kind, skip
		return nil
	}
}

// enrichSnapshot extracts symbols from all files in a snapshot.
func (e *Enricher) enrichSnapshot(snapshotID []byte) error {
	// Get the snapshot node
	content, kind, err := pack.ExtractObject(e.db, snapshotID)
	if err != nil {
		return err
	}
	if kind != "Snapshot" {
		return nil
	}

	// Parse snapshot payload to get file list
	var payload map[string]interface{}
	if err := json.Unmarshal(content, &payload); err != nil {
		return err
	}

	// Note: In a full implementation, we would:
	// 1. Look up all HAS_FILE edges from this snapshot
	// 2. For each file, extract symbols using the parser
	// 3. Create Symbol nodes and DEFINES_IN edges
	// 4. Store these back into the database
	//
	// For the MVP, we log what would happen and return success.
	log.Printf("would analyze symbols for snapshot %x (payload: %v)", snapshotID[:8], payload)

	return nil
}

// enrichChangeSet generates intent for a changeset.
func (e *Enricher) enrichChangeSet(changeSetID []byte) error {
	content, kind, err := pack.ExtractObject(e.db, changeSetID)
	if err != nil {
		return err
	}
	if kind != "ChangeSet" {
		return nil
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(content, &payload); err != nil {
		return err
	}

	// Check if intent already exists
	if existingIntent, ok := payload["intent"].(string); ok && existingIntent != "" {
		log.Printf("changeset %x already has intent: %s", changeSetID[:8], existingIntent)
		return nil
	}

	// In a full implementation, we would:
	// 1. Get all change types (HAS edges)
	// 2. Get affected modules (AFFECTS edges)
	// 3. Get modified symbols (MODIFIES edges)
	// 4. Call intent.GenerateIntent()
	// 5. Update the changeset payload with the intent
	//
	// For MVP, generate a placeholder intent
	generatedIntent := intent.GenerateIntent(nil, nil, nil, nil)
	log.Printf("generated intent for changeset %x: %s", changeSetID[:8], generatedIntent)

	return nil
}

// ProcessAll processes all pending enrichment items synchronously.
// Useful for testing.
func (e *Enricher) ProcessAll() error {
	for {
		item, err := e.db.ClaimEnrichmentItem()
		if err != nil {
			return err
		}
		if item == nil {
			return nil // No more work
		}

		errMsg := ""
		if err := e.enrich(item); err != nil {
			errMsg = err.Error()
		}

		if err := e.db.CompleteEnrichmentItem(item.ID, errMsg); err != nil {
			return err
		}
	}
}
