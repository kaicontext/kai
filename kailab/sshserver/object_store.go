package sshserver

import "sync"

// MemoryObjectStore caches git objects in memory by OID.
type MemoryObjectStore struct {
	mu    sync.RWMutex
	store map[string]GitObject
}

// NewMemoryObjectStore creates an empty in-memory object store.
func NewMemoryObjectStore() *MemoryObjectStore {
	return &MemoryObjectStore{store: make(map[string]GitObject)}
}

func (s *MemoryObjectStore) Get(oid string) (GitObject, bool) {
	s.mu.RLock()
	obj, ok := s.store[oid]
	s.mu.RUnlock()
	return obj, ok
}

func (s *MemoryObjectStore) Has(oid string) bool {
	s.mu.RLock()
	_, ok := s.store[oid]
	s.mu.RUnlock()
	return ok
}

func (s *MemoryObjectStore) Put(obj GitObject) {
	if obj.OID == "" {
		return
	}
	s.mu.Lock()
	s.store[obj.OID] = obj
	s.mu.Unlock()
}
