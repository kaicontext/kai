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
	refCommits, _, err := refAdapter.BuildRefCommits(ctx)
	if err != nil {
		return nil, err
	}

	objects := make([]GitObject, 0)
	seen := make(map[string]bool)

	for _, want := range wants {
		if haves != nil && haves[want] {
			continue
		}
		info, ok := refCommits[want]
		if !ok {
			return nil, fmt.Errorf("unknown want %s", want)
		}
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

	if len(objects) == 0 {
		objects = append(objects, buildEmptyTreeObject())
	}

	return objects, nil
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
