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

	objects, err := buildPackObjects(ctx, b.refAdapter, req.Wants)
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
