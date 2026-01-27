package sshserver

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

// parseGitPackWithBases extracts objects from a git pack, using existing objects for delta resolution.
// Returns a map from OID to GitObject.
func parseGitPackWithBases(data []byte, existingObjects map[string]GitObject) (map[string]GitObject, error) {
	return parseGitPackInternal(data, existingObjects)
}

// parseGitPack extracts objects from a git pack.
// Returns a map from OID to GitObject.
func parseGitPack(data []byte) (map[string]GitObject, error) {
	return parseGitPackInternal(data, nil)
}

func parseGitPackInternal(data []byte, existingObjects map[string]GitObject) (map[string]GitObject, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("pack too short")
	}

	// Verify header
	if string(data[0:4]) != "PACK" {
		return nil, fmt.Errorf("invalid pack header")
	}
	version := binary.BigEndian.Uint32(data[4:8])
	if version != 2 {
		return nil, fmt.Errorf("unsupported pack version %d", version)
	}
	numObjects := binary.BigEndian.Uint32(data[8:12])

	objects := make(map[string]GitObject)
	deltas := make([]deltaObject, 0)
	offset := 12

	for i := uint32(0); i < numObjects; i++ {
		if offset >= len(data)-20 { // Leave room for trailer
			return nil, fmt.Errorf("truncated pack at object %d", i)
		}

		objType, size, headerLen := parsePackObjectHeader(data[offset:])
		offset += headerLen

		switch objType {
		case 1, 2, 3: // commit, tree, blob
			content, consumed, err := zlibDecompress(data[offset:], size)
			if err != nil {
				return nil, fmt.Errorf("decompress object %d: %w", i, err)
			}
			offset += consumed

			obj := makeGitObject(ObjectType(objType), content)
			objects[obj.OID] = obj

		case 6: // OFS_DELTA
			// Read negative offset
			var deltaOffset int64
			for shift := uint(0); ; shift += 7 {
				if offset >= len(data) {
					return nil, fmt.Errorf("truncated ofs-delta offset")
				}
				b := data[offset]
				offset++
				deltaOffset |= int64(b&0x7f) << shift
				if b&0x80 == 0 {
					break
				}
				deltaOffset++
			}
			content, consumed, err := zlibDecompress(data[offset:], size)
			if err != nil {
				return nil, fmt.Errorf("decompress ofs-delta %d: %w", i, err)
			}
			offset += consumed
			deltas = append(deltas, deltaObject{
				deltaType:   6,
				baseOffset:  int(deltaOffset),
				deltaData:   content,
				startOffset: offset - consumed - headerLen,
			})

		case 7: // REF_DELTA
			if offset+20 > len(data) {
				return nil, fmt.Errorf("truncated ref-delta base")
			}
			baseOID := hex.EncodeToString(data[offset : offset+20])
			offset += 20
			content, consumed, err := zlibDecompress(data[offset:], size)
			if err != nil {
				return nil, fmt.Errorf("decompress ref-delta %d: %w", i, err)
			}
			offset += consumed
			deltas = append(deltas, deltaObject{
				deltaType: 7,
				baseOID:   baseOID,
				deltaData: content,
			})

		default:
			return nil, fmt.Errorf("unsupported object type %d", objType)
		}
	}

	// Apply deltas (simplified - may need multiple passes for chained deltas)
	for _, delta := range deltas {
		var baseObj GitObject
		var found bool

		if delta.deltaType == 7 {
			baseObj, found = objects[delta.baseOID]
			// Try existing objects if not found in current pack
			if !found && existingObjects != nil {
				baseObj, found = existingObjects[delta.baseOID]
			}
		}
		// Note: OFS_DELTA not fully implemented - would need offset tracking

		if !found {
			// Skip unresolvable delta (might be thin pack base)
			continue
		}

		result, err := applyDelta(baseObj.Data, delta.deltaData)
		if err != nil {
			return nil, fmt.Errorf("apply delta: %w", err)
		}

		obj := makeGitObject(baseObj.Type, result)
		objects[obj.OID] = obj
	}

	return objects, nil
}

type deltaObject struct {
	deltaType   int
	baseOffset  int
	baseOID     string
	deltaData   []byte
	startOffset int
}

func parsePackObjectHeader(data []byte) (objType int, size int, headerLen int) {
	b := data[0]
	objType = int((b >> 4) & 0x07)
	size = int(b & 0x0f)
	headerLen = 1
	shift := 4

	for b&0x80 != 0 {
		b = data[headerLen]
		size |= int(b&0x7f) << shift
		shift += 7
		headerLen++
	}

	return objType, size, headerLen
}

func zlibDecompress(data []byte, expectedSize int) ([]byte, int, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()

	result := make([]byte, expectedSize)
	n, err := io.ReadFull(r, result)
	if err != nil && err != io.EOF {
		return nil, 0, err
	}

	// Find how many bytes were consumed from input
	// This is tricky with zlib - use a counting reader
	consumed := findZlibEnd(data)

	return result[:n], consumed, nil
}

func findZlibEnd(data []byte) int {
	// Parse zlib stream to find end
	// zlib format: CMF (1 byte) + FLG (1 byte) + [DICTID (4 bytes)] + compressed data + ADLER32 (4 bytes)
	if len(data) < 6 {
		return len(data)
	}

	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return len(data)
	}
	io.Copy(io.Discard, r)
	r.Close()

	// Estimate consumed bytes by trying progressively smaller slices
	for i := 6; i <= len(data); i++ {
		r, err := zlib.NewReader(bytes.NewReader(data[:i]))
		if err != nil {
			continue
		}
		_, err = io.Copy(io.Discard, r)
		r.Close()
		if err == nil {
			return i
		}
	}

	return len(data)
}

func makeGitObject(objType ObjectType, data []byte) GitObject {
	typeName := ""
	switch objType {
	case ObjectCommit:
		typeName = "commit"
	case ObjectTree:
		typeName = "tree"
	case ObjectBlob:
		typeName = "blob"
	}

	oid := computeGitOID(typeName, data)
	return GitObject{
		Type: objType,
		Data: data,
		OID:  oid,
	}
}

func applyDelta(base, delta []byte) ([]byte, error) {
	if len(delta) < 2 {
		return nil, fmt.Errorf("delta too short")
	}

	// Read base size (variable-length encoding)
	baseSize, offset := readDeltaSize(delta, 0)
	if baseSize != len(base) {
		return nil, fmt.Errorf("base size mismatch: expected %d, got %d", baseSize, len(base))
	}

	// Read result size
	resultSize, offset := readDeltaSize(delta, offset)
	result := make([]byte, 0, resultSize)

	for offset < len(delta) {
		cmd := delta[offset]
		offset++

		if cmd&0x80 != 0 {
			// Copy from base
			copyOffset := 0
			copySize := 0

			if cmd&0x01 != 0 {
				copyOffset = int(delta[offset])
				offset++
			}
			if cmd&0x02 != 0 {
				copyOffset |= int(delta[offset]) << 8
				offset++
			}
			if cmd&0x04 != 0 {
				copyOffset |= int(delta[offset]) << 16
				offset++
			}
			if cmd&0x08 != 0 {
				copyOffset |= int(delta[offset]) << 24
				offset++
			}

			if cmd&0x10 != 0 {
				copySize = int(delta[offset])
				offset++
			}
			if cmd&0x20 != 0 {
				copySize |= int(delta[offset]) << 8
				offset++
			}
			if cmd&0x40 != 0 {
				copySize |= int(delta[offset]) << 16
				offset++
			}

			if copySize == 0 {
				copySize = 0x10000
			}

			if copyOffset+copySize > len(base) {
				return nil, fmt.Errorf("copy extends past base")
			}
			result = append(result, base[copyOffset:copyOffset+copySize]...)

		} else if cmd != 0 {
			// Insert from delta
			insertSize := int(cmd)
			if offset+insertSize > len(delta) {
				return nil, fmt.Errorf("insert extends past delta")
			}
			result = append(result, delta[offset:offset+insertSize]...)
			offset += insertSize
		}
	}

	return result, nil
}

func readDeltaSize(data []byte, offset int) (int, int) {
	size := 0
	shift := 0
	for {
		b := data[offset]
		offset++
		size |= int(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			break
		}
	}
	return size, offset
}

// findGitCommitOID finds the commit OID that matches the given SHA from a parsed pack.
func findGitCommitOID(objects map[string]GitObject, targetOID string) (GitObject, bool) {
	obj, ok := objects[targetOID]
	if ok && obj.Type == ObjectCommit {
		return obj, true
	}
	return GitObject{}, false
}

// extractTreeOID extracts tree OID from commit data.
func extractTreeOID(commitData []byte) string {
	lines := bytes.Split(commitData, []byte("\n"))
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("tree ")) {
			return string(line[5:45])
		}
	}
	return ""
}

// extractParentOIDs extracts parent commit OIDs from commit data.
func extractParentOIDs(commitData []byte) []string {
	var parents []string
	lines := bytes.Split(commitData, []byte("\n"))
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("parent ")) && len(line) >= 47 {
			parents = append(parents, string(line[7:47]))
		}
	}
	return parents
}

// collectCommitObjects collects commit, tree, and all blob objects reachable from a commit.
func collectCommitObjects(objects map[string]GitObject, commitOID string) []GitObject {
	result, _ := collectCommitObjectsWithDepth(objects, commitOID, 0)
	return result
}

// collectCommitObjectsWithDepth collects objects with optional depth limiting.
// maxDepth of 0 means unlimited. maxDepth of 1 means only the tip commit (shallow clone).
// Returns the collected objects and a list of shallow boundary commits (commits with parents not included).
func collectCommitObjectsWithDepth(objects map[string]GitObject, commitOID string, maxDepth int) ([]GitObject, []string) {
	result := make([]GitObject, 0)
	shallowCommits := make([]string, 0)
	visited := make(map[string]bool)
	commitDepths := make(map[string]int)

	var collectTree func(oid string)
	collectTree = func(oid string) {
		if visited[oid] {
			return
		}
		visited[oid] = true

		obj, ok := objects[oid]
		if !ok {
			return
		}
		result = append(result, obj)

		if obj.Type == ObjectTree {
			entries := parseTreeEntries(obj.Data)
			for _, entry := range entries {
				collectTree(entry.oid)
			}
		}
	}

	var collectCommit func(oid string, depth int)
	collectCommit = func(oid string, depth int) {
		if visited[oid] {
			return
		}
		visited[oid] = true
		commitDepths[oid] = depth

		obj, ok := objects[oid]
		if !ok {
			return
		}
		result = append(result, obj)

		if obj.Type != ObjectCommit {
			return
		}

		// Always collect the tree for this commit
		treeOID := extractTreeOID(obj.Data)
		if treeOID != "" {
			collectTree(treeOID)
		}

		// Check if we should traverse parent commits
		parentOIDs := extractParentOIDs(obj.Data)
		if len(parentOIDs) > 0 {
			if maxDepth > 0 && depth >= maxDepth {
				// Depth limit reached - this commit is a shallow boundary
				shallowCommits = append(shallowCommits, oid)
			} else {
				// Continue traversing parents
				for _, parentOID := range parentOIDs {
					collectCommit(parentOID, depth+1)
				}
			}
		}
	}

	collectCommit(commitOID, 1)
	return result, shallowCommits
}

type gitTreeEntry struct {
	mode string
	name string
	oid  string
}

func parseTreeEntries(data []byte) []gitTreeEntry {
	entries := make([]gitTreeEntry, 0)
	offset := 0

	for offset < len(data) {
		// Find space after mode
		spaceIdx := bytes.IndexByte(data[offset:], ' ')
		if spaceIdx < 0 {
			break
		}
		mode := string(data[offset : offset+spaceIdx])
		offset += spaceIdx + 1

		// Find null after name
		nullIdx := bytes.IndexByte(data[offset:], 0)
		if nullIdx < 0 {
			break
		}
		name := string(data[offset : offset+nullIdx])
		offset += nullIdx + 1

		// Read 20-byte SHA
		if offset+20 > len(data) {
			break
		}
		oid := hex.EncodeToString(data[offset : offset+20])
		offset += 20

		entries = append(entries, gitTreeEntry{mode: mode, name: name, oid: oid})
	}

	return entries
}

func computeGitOIDBytes(kind string, data []byte) []byte {
	header := []byte(fmt.Sprintf("%s %d\x00", kind, len(data)))
	sum := sha1.Sum(append(header, data...))
	return sum[:]
}
