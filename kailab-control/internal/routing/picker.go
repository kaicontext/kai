// Package routing provides shard routing and selection.
package routing

import (
	"hash/fnv"
	"sync"
)

// ShardPicker selects shards for new repos and routes requests.
type ShardPicker struct {
	mu     sync.RWMutex
	shards map[string]string // shard name -> URL
	names  []string          // ordered shard names for round-robin
	next   int               // next index for round-robin
}

// NewShardPicker creates a new shard picker.
func NewShardPicker(shards map[string]string) *ShardPicker {
	names := make([]string, 0, len(shards))
	for name := range shards {
		names = append(names, name)
	}
	return &ShardPicker{
		shards: shards,
		names:  names,
	}
}

// GetShardURL returns the URL for a shard.
func (p *ShardPicker) GetShardURL(shardName string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.shards[shardName]
}

// PickShardRoundRobin picks the next shard using round-robin.
func (p *ShardPicker) PickShardRoundRobin() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.names) == 0 {
		return "default"
	}

	shard := p.names[p.next]
	p.next = (p.next + 1) % len(p.names)
	return shard
}

// PickShardByHash picks a shard based on a hash of the org ID.
func (p *ShardPicker) PickShardByHash(orgID string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.names) == 0 {
		return "default"
	}

	h := fnv.New64a()
	h.Write([]byte(orgID))
	idx := h.Sum64() % uint64(len(p.names))
	return p.names[idx]
}

// UpdateShards updates the shard map.
func (p *ShardPicker) UpdateShards(shards map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.shards = shards
	p.names = make([]string, 0, len(shards))
	for name := range shards {
		p.names = append(p.names, name)
	}
}

// ListShards returns all shard names and URLs.
func (p *ShardPicker) ListShards() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make(map[string]string, len(p.shards))
	for k, v := range p.shards {
		result[k] = v
	}
	return result
}
