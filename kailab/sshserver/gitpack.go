package sshserver

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"kailab/pack"
	"kailab/store"
)

type gitObjectType int

const (
	gitObjectCommit gitObjectType = 1
	gitObjectTree   gitObjectType = 2
	gitObjectBlob   gitObjectType = 3
)

type gitObject struct {
	Type gitObjectType
	Data []byte
	OID  string
}

const emptyTreeOID = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func buildPackObjects(db *sql.DB, wants []string) ([]gitObject, error) {
	refCommits, _, err := buildRefCommits(db)
	if err != nil {
		return nil, err
	}

	objects := make([]gitObject, 0)
	seen := make(map[string]bool)

	for _, want := range wants {
		info, ok := refCommits[want]
		if !ok {
			return nil, fmt.Errorf("unknown want %s", want)
		}
		if !seen[info.commit.OID] {
			seen[info.commit.OID] = true
			objects = append(objects, info.commit)
		}
		for _, obj := range info.objects {
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

func buildEmptyTreeObject() gitObject {
	return gitObject{
		Type: gitObjectTree,
		Data: []byte{},
		OID:  emptyTreeOID,
	}
}

func buildCommitObject(refName, targetHex, treeOID string) gitObject {
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
	return gitObject{Type: gitObjectCommit, Data: data, OID: oid}
}

func writePack(w io.Writer, objects []gitObject) error {
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

func writePackObject(w *bytes.Buffer, obj gitObject) error {
	if obj.Type != gitObjectCommit && obj.Type != gitObjectTree && obj.Type != gitObjectBlob {
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

type refCommitInfo struct {
	commit  gitObject
	objects []gitObject
}

type refBuild struct {
	refName string
	commit  gitObject
	objects []gitObject
}

func buildRefCommits(db *sql.DB) (map[string]refCommitInfo, map[string]string, error) {
	refs, err := store.ListRefs(db, "")
	if err != nil {
		return nil, nil, fmt.Errorf("list refs: %w", err)
	}

	result := make(map[string]refCommitInfo)
	refToOID := make(map[string]string)
	cache := make(map[string]snapshotObjects)

	for _, ref := range refs {
		built, err := buildRef(db, ref, cache)
		if err != nil {
			return nil, nil, err
		}
		result[built.commit.OID] = refCommitInfo{
			commit:  built.commit,
			objects: built.objects,
		}
		refToOID[built.refName] = built.commit.OID
	}

	return result, refToOID, nil
}

type snapshotObjects struct {
	treeOID string
	objects []gitObject
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
	refName := mapRefName(ref.Name)
	targetHex := hex.EncodeToString(ref.Target)

	snapshotDigest, err := resolveSnapshotDigest(db, ref.Target)
	if err != nil {
		return refBuild{}, err
	}

	var objects snapshotObjects
	if snapshotDigest != nil {
		key := hex.EncodeToString(snapshotDigest)
		if cached, ok := cache[key]; ok {
			objects = cached
		} else {
			built, err := buildSnapshotObjects(db, snapshotDigest)
			if err != nil {
				return refBuild{}, err
			}
			cache[key] = built
			objects = built
		}
	} else {
		objects = snapshotObjects{
			treeOID: emptyTreeOID,
			objects: []gitObject{buildEmptyTreeObject()},
		}
	}

	commit := buildCommitObject(refName, targetHex, objects.treeOID)
	return refBuild{
		refName: refName,
		commit:  commit,
		objects: objects.objects,
	}, nil
}

func buildSnapshotObjects(db *sql.DB, snapshotDigest []byte) (snapshotObjects, error) {
	files, err := getSnapshotFiles(db, snapshotDigest)
	if err != nil {
		return snapshotObjects{}, err
	}

	tree, blobs, err := buildTreeFromFiles(db, files)
	if err != nil {
		return snapshotObjects{}, err
	}

	objects := append([]gitObject{}, blobs...)
	objects = append(objects, tree.objects...)

	return snapshotObjects{
		treeOID: tree.oid,
		objects: objects,
	}, nil
}

type snapshotFile struct {
	Path          string
	ContentDigest string
}

func getSnapshotFiles(db *sql.DB, snapshotDigest []byte) ([]snapshotFile, error) {
	content, kind, err := pack.ExtractObjectFromDB(db, snapshotDigest)
	if err != nil {
		return nil, err
	}
	if kind != "Snapshot" {
		return nil, fmt.Errorf("not a snapshot")
	}

	payload, err := parsePayload(content)
	if err != nil {
		return nil, err
	}

	var snapshotPayload struct {
		FileDigests []string `json:"fileDigests"`
		Files       []struct {
			Path          string `json:"path"`
			Digest        string `json:"digest"`
			ContentDigest string `json:"contentDigest"`
		} `json:"files"`
	}
	if err := json.Unmarshal(payload, &snapshotPayload); err != nil {
		return nil, err
	}

	if len(snapshotPayload.Files) > 0 {
		files := make([]snapshotFile, 0, len(snapshotPayload.Files))
		for _, f := range snapshotPayload.Files {
			contentDigest := f.ContentDigest
			if contentDigest == "" {
				contentDigest = f.Digest
			}
			files = append(files, snapshotFile{
				Path:          f.Path,
				ContentDigest: contentDigest,
			})
		}
		return files, nil
	}

	var files []snapshotFile
	for _, fileDigestHex := range snapshotPayload.FileDigests {
		fileDigest, err := hex.DecodeString(fileDigestHex)
		if err != nil {
			continue
		}
		fileContent, fileKind, err := pack.ExtractObjectFromDB(db, fileDigest)
		if err != nil || fileKind != "File" {
			continue
		}
		filePayload, err := parsePayload(fileContent)
		if err != nil {
			continue
		}
		var file struct {
			Path   string `json:"path"`
			Digest string `json:"digest"`
		}
		if err := json.Unmarshal(filePayload, &file); err != nil {
			continue
		}
		files = append(files, snapshotFile{
			Path:          file.Path,
			ContentDigest: file.Digest,
		})
	}
	return files, nil
}

func parsePayload(content []byte) ([]byte, error) {
	if idx := bytes.IndexByte(content, '\n'); idx >= 0 {
		return content[idx+1:], nil
	}
	return content, nil
}

type treeBuildResult struct {
	oid     string
	objects []gitObject
}

func buildTreeFromFiles(db *sql.DB, files []snapshotFile) (treeBuildResult, []gitObject, error) {
	root := &treeNode{
		dirs:  map[string]*treeNode{},
		blobs: map[string]string{},
	}
	var blobs []gitObject

	for _, file := range files {
		if file.Path == "" || file.ContentDigest == "" {
			continue
		}
		contentDigest, err := hex.DecodeString(file.ContentDigest)
		if err != nil {
			continue
		}
		content, _, err := pack.ExtractObjectFromDB(db, contentDigest)
		if err != nil {
			continue
		}
		blobOID := computeGitOID("blob", content)
		blobs = append(blobs, gitObject{Type: gitObjectBlob, Data: content, OID: blobOID})

		insertPath(root, filepath.ToSlash(file.Path), blobOID)
	}

	tree, err := buildTreeObjects(root)
	if err != nil {
		return treeBuildResult{}, nil, err
	}
	return tree, blobs, nil
}

type treeNode struct {
	dirs  map[string]*treeNode
	blobs map[string]string
}

func insertPath(root *treeNode, path string, blobOID string) {
	parts := strings.Split(path, "/")
	node := root
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if part == "" {
			continue
		}
		child, ok := node.dirs[part]
		if !ok {
			child = &treeNode{dirs: map[string]*treeNode{}, blobs: map[string]string{}}
			node.dirs[part] = child
		}
		node = child
	}
	name := parts[len(parts)-1]
	if name != "" {
		node.blobs[name] = blobOID
	}
}

func buildTreeObjects(node *treeNode) (treeBuildResult, error) {
	var entries []treeEntry
	var objects []gitObject

	for name, child := range node.dirs {
		result, err := buildTreeObjects(child)
		if err != nil {
			return treeBuildResult{}, err
		}
		objects = append(objects, result.objects...)
		entries = append(entries, treeEntry{
			mode: "40000",
			name: name,
			oid:  result.oid,
		})
	}

	for name, oid := range node.blobs {
		entries = append(entries, treeEntry{
			mode: "100644",
			name: name,
			oid:  oid,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})

	var data bytes.Buffer
	for _, entry := range entries {
		fmt.Fprintf(&data, "%s %s\x00", entry.mode, entry.name)
		oidBytes, err := hex.DecodeString(entry.oid)
		if err != nil || len(oidBytes) != 20 {
			return treeBuildResult{}, fmt.Errorf("invalid oid %s", entry.oid)
		}
		data.Write(oidBytes)
	}

	oid := computeGitOID("tree", data.Bytes())
	tree := gitObject{Type: gitObjectTree, Data: data.Bytes(), OID: oid}
	objects = append(objects, tree)
	return treeBuildResult{oid: oid, objects: objects}, nil
}

type treeEntry struct {
	mode string
	name string
	oid  string
}
