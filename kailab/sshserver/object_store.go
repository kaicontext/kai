package sshserver

import (
	"container/list"
	"sync"
)

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

// LRUObjectStore caches git objects with a max entry count.
type LRUObjectStore struct {
	mu    sync.Mutex
	max   int
	items map[string]*list.Element
	order *list.List
}

type lruEntry struct {
	oid string
	obj GitObject
}

// NewLRUObjectStore creates an LRU cache with a max entry count.
func NewLRUObjectStore(maxEntries int) *LRUObjectStore {
	if maxEntries < 0 {
		maxEntries = 0
	}
	return &LRUObjectStore{
		max:   maxEntries,
		items: make(map[string]*list.Element),
		order: list.New(),
	}
}

func (s *LRUObjectStore) Get(oid string) (GitObject, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	element, ok := s.items[oid]
	if !ok {
		return GitObject{}, false
	}
	s.order.MoveToFront(element)
	entry := element.Value.(lruEntry)
	return entry.obj, true
}

func (s *LRUObjectStore) Has(oid string) bool {
	s.mu.Lock()
	_, ok := s.items[oid]
	s.mu.Unlock()
	return ok
}

func (s *LRUObjectStore) Put(obj GitObject) {
	if obj.OID == "" || s.max == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if element, ok := s.items[obj.OID]; ok {
		element.Value = lruEntry{oid: obj.OID, obj: obj}
		s.order.MoveToFront(element)
		return
	}

	element := s.order.PushFront(lruEntry{oid: obj.OID, obj: obj})
	s.items[obj.OID] = element

	for s.max > 0 && s.order.Len() > s.max {
		last := s.order.Back()
		if last == nil {
			break
		}
		entry := last.Value.(lruEntry)
		delete(s.items, entry.oid)
		s.order.Remove(last)
	}
}
