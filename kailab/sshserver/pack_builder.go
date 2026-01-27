package sshserver

import (
	"context"
	"fmt"
	"io"
)

// DefaultPackBuilder builds packs from refs using adapters and an object store.
type DefaultPackBuilder struct {
	refAdapter RefAdapter
	store      ObjectStore
}

// NewPackBuilder constructs a pack builder.
func NewPackBuilder(refAdapter RefAdapter, store ObjectStore) *DefaultPackBuilder {
	return &DefaultPackBuilder{
		refAdapter: refAdapter,
		store:      store,
	}
}

// BuildPack builds a pack file and writes it to w.
// Returns PackResult with metadata about shallow boundaries.
func (b *DefaultPackBuilder) BuildPack(ctx context.Context, req PackRequest, w io.Writer) (*PackResult, error) {
	result := &PackResult{}

	if len(req.Wants) == 0 {
		return result, writeEmptyPack(w)
	}
	if b.refAdapter == nil {
		return nil, fmt.Errorf("ref adapter required")
	}

	haves := make(map[string]bool, len(req.Haves))
	for _, have := range req.Haves {
		if have != "" {
			haves[have] = true
		}
	}

	var objects []GitObject
	var err error

	if req.Depth > 0 {
		// Shallow clone - use depth limiting
		objects, result.ShallowCommits, err = buildPackObjectsWithDepth(ctx, b.refAdapter, req.Wants, haves, req.Depth)
	} else {
		objects, err = buildPackObjects(ctx, b.refAdapter, req.Wants, haves)
	}
	if err != nil {
		return nil, err
	}

	if b.store != nil {
		for _, obj := range objects {
			b.store.Put(obj)
		}
	}

	// If thin-pack is requested and we have base objects, try to generate deltas
	if req.ThinPack && len(req.Haves) > 0 && b.store != nil {
		candidates := b.buildDeltaCandidates(objects, req.Haves)
		return result, writePackWithDeltas(w, candidates)
	}

	return result, writePack(w, objects)
}

// buildDeltaCandidates attempts to create delta candidates for objects.
// It looks for similar objects in the "haves" list to use as bases.
func (b *DefaultPackBuilder) buildDeltaCandidates(objects []GitObject, haves []string) []DeltaCandidate {
	candidates := make([]DeltaCandidate, len(objects))

	// Build a map of base objects we can use (from haves)
	baseObjects := make(map[string]GitObject)
	for _, oid := range haves {
		if obj, ok := b.store.Get(oid); ok {
			baseObjects[oid] = obj
		}
	}

	// Also collect objects we're sending (can use as bases for later objects)
	for _, obj := range objects {
		baseObjects[obj.OID] = obj
	}

	for i, obj := range objects {
		candidates[i] = DeltaCandidate{Object: obj}

		// Only deltify blobs and trees (not commits)
		if obj.Type != ObjectBlob && obj.Type != ObjectTree {
			continue
		}

		// Find best base object of same type
		var bestBase GitObject
		var bestDelta []byte

		for _, base := range baseObjects {
			// Skip self and different types
			if base.OID == obj.OID || base.Type != obj.Type {
				continue
			}

			delta := GenerateDelta(base.Data, obj.Data)
			if delta != nil {
				if bestDelta == nil || len(delta) < len(bestDelta) {
					bestBase = base
					bestDelta = delta
				}
			}
		}

		if bestDelta != nil {
			candidates[i].BaseOID = bestBase.OID
			candidates[i].Delta = bestDelta
		}
	}

	return candidates
}
