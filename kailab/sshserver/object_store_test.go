package sshserver

import "testing"

func TestMemoryObjectStore(t *testing.T) {
	store := NewMemoryObjectStore()

	if store.Has("deadbeef") {
		t.Fatal("expected empty store")
	}
	if _, ok := store.Get("deadbeef"); ok {
		t.Fatal("expected missing object")
	}

	obj := GitObject{Type: ObjectBlob, Data: []byte("hello"), OID: "deadbeef"}
	store.Put(obj)

	if !store.Has("deadbeef") {
		t.Fatal("expected object to exist")
	}
	got, ok := store.Get("deadbeef")
	if !ok {
		t.Fatal("expected to retrieve object")
	}
	if got.OID != obj.OID || got.Type != obj.Type {
		t.Fatalf("unexpected object: %+v", got)
	}
}

func TestLRUObjectStore(t *testing.T) {
	store := NewLRUObjectStore(2)
	store.Put(GitObject{Type: ObjectBlob, Data: []byte("a"), OID: "a"})
	store.Put(GitObject{Type: ObjectBlob, Data: []byte("b"), OID: "b"})

	if !store.Has("a") || !store.Has("b") {
		t.Fatal("expected initial entries")
	}

	store.Put(GitObject{Type: ObjectBlob, Data: []byte("c"), OID: "c"})
	if store.Has("a") {
		t.Fatal("expected LRU eviction of a")
	}
	if !store.Has("b") || !store.Has("c") {
		t.Fatal("expected b and c to remain")
	}
}
