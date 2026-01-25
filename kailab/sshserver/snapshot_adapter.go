package sshserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"kailab/pack"
)

// DBSnapshotAdapter translates Kai snapshots/changesets into git trees/blobs.
type DBSnapshotAdapter struct {
	db *sql.DB
}

// NewDBSnapshotAdapter returns a snapshot adapter backed by the Kai store.
func NewDBSnapshotAdapter(db *sql.DB) *DBSnapshotAdapter {
	return &DBSnapshotAdapter{db: db}
}

func (a *DBSnapshotAdapter) SnapshotObjects(ctx context.Context, snapshotDigest []byte) (string, []GitObject, error) {
	files, err := getSnapshotFiles(a.db, snapshotDigest)
	if err != nil {
		return "", nil, err
	}

	tree, blobs, err := buildTreeFromFiles(a.db, files)
	if err != nil {
		return "", nil, err
	}

	objects := append([]GitObject{}, blobs...)
	objects = append(objects, tree.objects...)
	return tree.oid, objects, nil
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

type snapshotFile struct {
	Path          string
	ContentDigest string
}

type treeBuildResult struct {
	oid     string
	objects []GitObject
}

func buildTreeFromFiles(db *sql.DB, files []snapshotFile) (treeBuildResult, []GitObject, error) {
	root := &treeNode{
		dirs:  map[string]*treeNode{},
		blobs: map[string]string{},
	}
	var blobs []GitObject

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
		blobs = append(blobs, GitObject{Type: ObjectBlob, Data: content, OID: blobOID})

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

type treeEntry struct {
	mode string
	name string
	oid  string
}

func buildTreeObjects(node *treeNode) (treeBuildResult, error) {
	var entries []treeEntry
	var objects []GitObject

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
	tree := GitObject{Type: ObjectTree, Data: data.Bytes(), OID: oid}
	objects = append(objects, tree)
	return treeBuildResult{oid: oid, objects: objects}, nil
}
