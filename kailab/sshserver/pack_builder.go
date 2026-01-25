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

func (b *DefaultPackBuilder) BuildPack(ctx context.Context, req PackRequest, w io.Writer) error {
	if len(req.Wants) == 0 {
		return writeEmptyPack(w)
	}
	if b.refAdapter == nil {
		return fmt.Errorf("ref adapter required")
	}

	// TODO: support thin packs / deltas when we add pack heuristics.
	haves := make(map[string]bool, len(req.Haves))
	for _, have := range req.Haves {
		if have != "" {
			haves[have] = true
		}
	}
	objects, err := buildPackObjects(ctx, b.refAdapter, req.Wants, haves)
	if err != nil {
		return err
	}

	if b.store != nil {
		for _, obj := range objects {
			b.store.Put(obj)
		}
	}

	return writePack(w, objects)
}
