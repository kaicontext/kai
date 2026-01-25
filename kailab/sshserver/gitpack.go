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

	content, kind, err := pack.ExtractObjectFromDB(db, target)
	if err != nil {
		return nil, err
	}
	if kind == "Snapshot" {
		return target, nil
	}

	if kind == "ChangeSet" {
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
		return hex.DecodeString(cs.Head)
	}

	return nil, nil
}

func buildRef(db *sql.DB, ref *store.Ref, cache map[string]snapshotObjects) (refBuild, error) {
	refName := MapRefName(ref.Name)
	targetHex := hex.EncodeToString(ref.Target)

	snapshotDigest, err := resolveSnapshotDigest(db, ref.Target)
	if err != nil {
		return refBuild{}, err
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
