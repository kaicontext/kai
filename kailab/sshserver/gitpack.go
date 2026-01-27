package sshserver

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"kailab/pack"
	"kailab/store"
)

const emptyTreeOID = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func buildPackObjects(ctx context.Context, refAdapter RefAdapter, wants []string, haves map[string]bool) ([]GitObject, error) {
	objects, _, err := buildPackObjectsWithDepth(ctx, refAdapter, wants, haves, 0)
	return objects, err
}

// buildPackObjectsWithDepth builds pack objects with optional depth limiting for shallow clones.
// depth of 0 means unlimited, depth of 1 means only the tip commit (shallow clone).
// Returns objects, shallow boundary commits, and any error.
func buildPackObjectsWithDepth(ctx context.Context, refAdapter RefAdapter, wants []string, haves map[string]bool, depth int) ([]GitObject, []string, error) {
	refCommits, _, err := refAdapter.BuildRefCommits(ctx)
	if err != nil {
		return nil, nil, err
	}

	objects := make([]GitObject, 0)
	shallowCommits := make([]string, 0)
	seen := make(map[string]bool)

	for _, want := range wants {
		if haves != nil && haves[want] {
			continue
		}
		info, ok := refCommits[want]
		if !ok {
			return nil, nil, fmt.Errorf("unknown want %s", want)
		}

		if depth > 0 {
			// For shallow clones, we need to re-collect with depth limiting
			// First, build a map of all available objects
			allObjects := make(map[string]GitObject)
			allObjects[info.Commit.OID] = info.Commit
			for _, obj := range info.Objects {
				allObjects[obj.OID] = obj
			}

			// Collect with depth limiting
			collected, shallow := collectCommitObjectsWithDepth(allObjects, info.Commit.OID, depth)
			for _, obj := range collected {
				if !seen[obj.OID] {
					seen[obj.OID] = true
					objects = append(objects, obj)
				}
			}
			for _, oid := range shallow {
				shallowCommits = append(shallowCommits, oid)
			}
		} else {
			// No depth limit - include all objects
			if !seen[info.Commit.OID] {
				seen[info.Commit.OID] = true
				objects = append(objects, info.Commit)
			}
			for _, obj := range info.Objects {
				if !seen[obj.OID] {
					seen[obj.OID] = true
					objects = append(objects, obj)
				}
			}
		}
	}

	if len(objects) == 0 {
		objects = append(objects, buildEmptyTreeObject())
	}

	return objects, shallowCommits, nil
}

func buildEmptyTreeObject() GitObject {
	return GitObject{
		Type: ObjectTree,
		Data: []byte{},
		OID:  emptyTreeOID,
	}
}

func buildCommitObject(refName, targetHex, treeOID string) GitObject {
	body := strings.Builder{}
	body.WriteString("tree " + treeOID + "\n")
	body.WriteString("author Kai <kai@local> 0 +0000\n")
	body.WriteString("committer Kai <kai@local> 0 +0000\n\n")
	body.WriteString("Kai ref " + refName + "\n")
	if targetHex != "" {
		body.WriteString("target " + targetHex + "\n")
	}
	data := []byte(body.String())
	oid := computeGitOID("commit", data)
	return GitObject{Type: ObjectCommit, Data: data, OID: oid}
}

func writePack(w io.Writer, objects []GitObject) error {
	var buf bytes.Buffer

	// Pack header
	buf.Write([]byte{'P', 'A', 'C', 'K'})
	buf.Write([]byte{0, 0, 0, 2}) // version 2
	count := uint32(len(objects))
	buf.Write([]byte{byte(count >> 24), byte(count >> 16), byte(count >> 8), byte(count)})

	for _, obj := range objects {
		if err := writePackObject(&buf, obj); err != nil {
			return err
		}
	}

	sum := sha1.Sum(buf.Bytes())
	if _, err := w.Write(buf.Bytes()); err != nil {
		return err
	}
	_, err := w.Write(sum[:])
	return err
}

func writePackObject(w *bytes.Buffer, obj GitObject) error {
	if obj.Type != ObjectCommit && obj.Type != ObjectTree && obj.Type != ObjectBlob {
		return fmt.Errorf("unsupported git object type %d", obj.Type)
	}

	size := len(obj.Data)
	header := encodeObjectHeader(int(obj.Type), size)
	if _, err := w.Write(header); err != nil {
		return err
	}

	zw := zlib.NewWriter(w)
	if _, err := zw.Write(obj.Data); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

// ObjectRefDelta is the Git pack object type for REF_DELTA (type 7)
const ObjectRefDelta = 7

// writePackRefDelta writes a REF_DELTA object to the pack.
// baseOID is the 40-character hex OID of the base object.
// delta is the delta data that transforms base into the target object.
func writePackRefDelta(w *bytes.Buffer, baseOID string, delta []byte) error {
	// Decode base OID from hex
	baseBytes, err := hex.DecodeString(baseOID)
	if err != nil || len(baseBytes) != 20 {
		return fmt.Errorf("invalid base OID: %s", baseOID)
	}

	// Write header: type 7 (REF_DELTA) + delta size
	header := encodeObjectHeader(ObjectRefDelta, len(delta))
	if _, err := w.Write(header); err != nil {
		return err
	}

	// Write base object OID (20 bytes, raw)
	if _, err := w.Write(baseBytes); err != nil {
		return err
	}

	// Write zlib-compressed delta data
	zw := zlib.NewWriter(w)
	if _, err := zw.Write(delta); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

// DeltaCandidate represents an object that could be deltified.
type DeltaCandidate struct {
	Object  GitObject
	BaseOID string // OID of base object to delta against (empty if none)
	Delta   []byte // Generated delta (nil if sending full object)
}

// writePackWithDeltas writes a pack that may contain delta objects.
func writePackWithDeltas(w io.Writer, candidates []DeltaCandidate) error {
	var buf bytes.Buffer

	// Pack header
	buf.Write([]byte{'P', 'A', 'C', 'K'})
	buf.Write([]byte{0, 0, 0, 2}) // version 2
	count := uint32(len(candidates))
	buf.Write([]byte{byte(count >> 24), byte(count >> 16), byte(count >> 8), byte(count)})

	for _, cand := range candidates {
		if cand.Delta != nil && cand.BaseOID != "" {
			// Write as REF_DELTA
			if err := writePackRefDelta(&buf, cand.BaseOID, cand.Delta); err != nil {
				return err
			}
		} else {
			// Write as full object
			if err := writePackObject(&buf, cand.Object); err != nil {
				return err
			}
		}
	}

	sum := sha1.Sum(buf.Bytes())
	if _, err := w.Write(buf.Bytes()); err != nil {
		return err
	}
	_, err := w.Write(sum[:])
	return err
}

func encodeObjectHeader(objType int, size int) []byte {
	var out []byte
	first := byte(objType<<4) | byte(size&0x0f)
	size >>= 4
	if size > 0 {
		first |= 0x80
	}
	out = append(out, first)
	for size > 0 {
		b := byte(size & 0x7f)
		size >>= 7
		if size > 0 {
			b |= 0x80
		}
		out = append(out, b)
	}
	return out
}

func computeGitOID(kind string, data []byte) string {
	header := []byte(fmt.Sprintf("%s %d\x00", kind, len(data)))
	sum := sha1.Sum(append(header, data...))
	return hex.EncodeToString(sum[:])
}

type refBuild struct {
	refName string
	commit  GitObject
	objects []GitObject
}

func buildRefCommits(db *sql.DB) (map[string]RefCommitInfo, map[string]string, error) {
	refs, err := store.ListRefs(db, "")
	if err != nil {
		return nil, nil, fmt.Errorf("list refs: %w", err)
	}

	result := make(map[string]RefCommitInfo)
	refToOID := make(map[string]string)
	cache := make(map[string]snapshotObjects)

	for _, ref := range refs {
		built, err := buildRef(db, ref, cache)
		if err != nil {
			return nil, nil, err
		}
		result[built.commit.OID] = RefCommitInfo{
			Commit:  built.commit,
			Objects: built.objects,
		}
		refToOID[built.refName] = built.commit.OID
	}

	return result, refToOID, nil
}

type snapshotObjects struct {
	treeOID string
	objects []GitObject
}

func resolveSnapshotDigest(db *sql.DB, target []byte) ([]byte, error) {
	if len(target) == 0 {
		return nil, nil
	}

	// Follow changeset chain up to 10 levels to find a snapshot
	current := target
	for i := 0; i < 10; i++ {
		content, kind, err := pack.ExtractObjectFromDB(db, current)
		if err != nil {
			return nil, err
		}
		if kind == "Snapshot" {
			return current, nil
		}

		if kind != "ChangeSet" {
			return nil, nil
		}

		payload, err := parsePayload(content)
		if err != nil {
			return nil, err
		}
		var cs struct {
			Head string `json:"head"`
		}
		if err := json.Unmarshal(payload, &cs); err != nil {
			return nil, err
		}
		if cs.Head == "" {
			return nil, nil
		}
		current, err = hex.DecodeString(cs.Head)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil // Max depth reached
}

func buildRef(db *sql.DB, ref *store.Ref, cache map[string]snapshotObjects) (refBuild, error) {
	refName := MapRefName(ref.Name)
	targetHex := hex.EncodeToString(ref.Target)

	// Try to resolve git objects from ChangeSet's gitPack first
	gitObjs, gitCommitOID, err := resolveGitPackObjects(db, ref.Target, refName)
	if err == nil && len(gitObjs) > 0 && gitCommitOID != "" {
		// Use actual git objects from the pack
		var commit GitObject
		var objects []GitObject
		for _, obj := range gitObjs {
			if obj.OID == gitCommitOID {
				commit = obj
			} else {
				objects = append(objects, obj)
			}
		}
		if commit.OID != "" {
			return refBuild{
				refName: refName,
				commit:  commit,
				objects: objects,
			}, nil
		}
	}

	// Fall back to synthetic commits from snapshots
	snapshotDigest, err := resolveSnapshotDigest(db, ref.Target)
	if err != nil {
		// If both git pack and snapshot resolution fail, use empty tree
		snapshotDigest = nil
	}

	snapshotAdapter := NewDBSnapshotAdapter(db)
	var objects snapshotObjects
	if snapshotDigest != nil {
		key := hex.EncodeToString(snapshotDigest)
		if cached, ok := cache[key]; ok {
			objects = cached
		} else {
			treeOID, objs, err := snapshotAdapter.SnapshotObjects(context.Background(), snapshotDigest)
			if err != nil {
				return refBuild{}, err
			}
			cache[key] = snapshotObjects{treeOID: treeOID, objects: objs}
			objects = cache[key]
		}
	} else {
		objects = snapshotObjects{
			treeOID: emptyTreeOID,
			objects: []GitObject{buildEmptyTreeObject()},
		}
	}

	commit := buildCommitObject(refName, targetHex, objects.treeOID)
	return refBuild{
		refName: refName,
		commit:  commit,
		objects: objects.objects,
	}, nil
}

// changesetPackInfo holds pack data collected from a changeset
type changesetPackInfo struct {
	packDigest []byte
	gitUpdates []map[string]interface{}
}

// resolveGitPackObjects attempts to retrieve git objects from a ChangeSet's gitPack.
// Returns the objects and the commit OID for the given ref.
// It follows the changeset chain to collect objects from all packs for proper delta resolution.
func resolveGitPackObjects(db *sql.DB, target []byte, refName string) ([]GitObject, string, error) {
	if len(target) == 0 {
		return nil, "", fmt.Errorf("empty target")
	}

	// First pass: collect all changeset pack info (newest to oldest)
	var changesets []changesetPackInfo
	var commitOID string
	current := target

	for i := 0; i < 20; i++ { // Max depth
		content, kind, err := pack.ExtractObjectFromDB(db, current)
		if err != nil {
			break
		}

		if kind != "ChangeSet" {
			break
		}

		payload, err := parsePayload(content)
		if err != nil {
			break
		}

		var cs struct {
			GitPack    string                   `json:"gitPack"`
			GitUpdates []map[string]interface{} `json:"gitUpdates"`
			Head       string                   `json:"head"`
		}
		if err := json.Unmarshal(payload, &cs); err != nil {
			break
		}

		// Find commit OID from first changeset (the one we're cloning)
		if commitOID == "" {
			for _, update := range cs.GitUpdates {
				updateRef, _ := update["ref"].(string)
				newOID, _ := update["new"].(string)
				if updateRef == refName && newOID != "" && newOID != strings.Repeat("0", 40) {
					commitOID = newOID
					break
				}
			}
		}

		// Collect pack info
		if cs.GitPack != "" {
			packDigest, err := hex.DecodeString(cs.GitPack)
			if err == nil {
				changesets = append(changesets, changesetPackInfo{
					packDigest: packDigest,
					gitUpdates: cs.GitUpdates,
				})
			}
		}

		// Follow chain to previous changeset
		if cs.Head == "" {
			break
		}
		current, err = hex.DecodeString(cs.Head)
		if err != nil {
			break
		}
	}

	if commitOID == "" {
		return nil, "", fmt.Errorf("no commit OID for ref %s", refName)
	}

	if len(changesets) == 0 {
		return nil, "", fmt.Errorf("no packs found")
	}

	// Second pass: parse packs in reverse order (oldest to newest)
	// This ensures base objects are available for delta resolution
	allObjects := make(map[string]GitObject)

	for i := len(changesets) - 1; i >= 0; i-- {
		csInfo := changesets[i]
		packContent, _, err := pack.ExtractObjectFromDB(db, csInfo.packDigest)
		if err != nil {
			continue
		}

		// Skip "GitPack\n" prefix
		if len(packContent) > 8 && string(packContent[:8]) == "GitPack\n" {
			packContent = packContent[8:]
		}

		objects, err := parseGitPackWithBases(packContent, allObjects)
		if err == nil {
			for oid, obj := range objects {
				allObjects[oid] = obj
			}
		}
	}

	if len(allObjects) == 0 {
		return nil, "", fmt.Errorf("no objects found")
	}

	// Collect all objects reachable from the commit
	result := collectCommitObjects(allObjects, commitOID)
	return result, commitOID, nil
}
