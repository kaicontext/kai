// Package pack handles segment pack ingestion and extraction.
package pack

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"kai-core/cas"
	"kailab/proto"
	"kailab/store"
)

// Pack format:
// [4 bytes: header length (big-endian)]
// [header JSON: PackHeader]
// [object data...]
//
// The header describes each object's digest, kind, offset (relative to data start), and length.
// Object data follows immediately after the header.

const (
	HeaderLengthSize = 4
	MaxHeaderSize    = 10 * 1024 * 1024 // 10MB max header
)

// IngestSegment ingests a zstd-compressed pack from a reader.
// It decompresses, parses the header, stores the segment, and indexes all objects.
func IngestSegment(db *store.DB, r io.Reader, actor string) (segmentID int64, indexed int, err error) {
	// Decompress the entire stream
	decoder, err := zstd.NewReader(r)
	if err != nil {
		return 0, 0, fmt.Errorf("creating zstd decoder: %w", err)
	}
	defer decoder.Close()

	// Read all decompressed content
	decompressed, err := io.ReadAll(decoder)
	if err != nil {
		return 0, 0, fmt.Errorf("decompressing: %w", err)
	}

	if len(decompressed) < HeaderLengthSize {
		return 0, 0, fmt.Errorf("pack too small: %d bytes", len(decompressed))
	}

	// Parse header length
	headerLen := binary.BigEndian.Uint32(decompressed[:HeaderLengthSize])
	if headerLen > MaxHeaderSize {
		return 0, 0, fmt.Errorf("header too large: %d bytes", headerLen)
	}
	if int(HeaderLengthSize+headerLen) > len(decompressed) {
		return 0, 0, fmt.Errorf("header length exceeds pack size")
	}

	// Parse header JSON
	headerData := decompressed[HeaderLengthSize : HeaderLengthSize+headerLen]
	var header proto.PackHeader
	if err := json.Unmarshal(headerData, &header); err != nil {
		return 0, 0, fmt.Errorf("parsing header: %w", err)
	}

	// Data starts after header
	dataStart := int64(HeaderLengthSize + headerLen)
	objectData := decompressed[dataStart:]

	// Compute checksum of the entire decompressed content
	checksum := cas.Blake3Hash(decompressed)

	// Begin transaction
	tx, err := db.BeginTx()
	if err != nil {
		return 0, 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert segment (store only the object data portion, not the header)
	segmentID, err = db.InsertSegment(tx, checksum, objectData)
	if err != nil {
		return 0, 0, fmt.Errorf("inserting segment: %w", err)
	}

	// Index each object
	for _, obj := range header.Objects {
		if obj.Offset+obj.Length > int64(len(objectData)) {
			return 0, 0, fmt.Errorf("object %x extends beyond data", obj.Digest[:8])
		}

		// Verify digest
		content := objectData[obj.Offset : obj.Offset+obj.Length]
		computedDigest := cas.Blake3Hash(content)
		if !bytes.Equal(computedDigest, obj.Digest) {
			return 0, 0, fmt.Errorf("digest mismatch for object at offset %d", obj.Offset)
		}

		err = db.InsertObject(tx, obj.Digest, segmentID, obj.Offset, obj.Length, obj.Kind)
		if err != nil {
			return 0, 0, fmt.Errorf("inserting object: %w", err)
		}

		// Record node publish for semantic objects
		if isSemanticKind(obj.Kind) {
			if err := db.RecordNodePublish(tx, obj.Digest, obj.Kind, actor); err != nil {
				return 0, 0, fmt.Errorf("recording node publish: %w", err)
			}
			// Enqueue snapshots for enrichment
			if obj.Kind == "Snapshot" {
				if err := db.EnqueueForEnrichment(tx, obj.Digest, obj.Kind); err != nil {
					return 0, 0, fmt.Errorf("enqueueing for enrichment: %w", err)
				}
			}
		}

		indexed++
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("committing transaction: %w", err)
	}

	return segmentID, indexed, nil
}

// BuildPack creates a zstd-compressed pack from objects.
// Each object is provided as digest, kind, and content.
func BuildPack(objects []PackObject) ([]byte, error) {
	var header proto.PackHeader
	var data bytes.Buffer

	// Build header and concatenate data
	for _, obj := range objects {
		entry := proto.PackObjectEntry{
			Digest: obj.Digest,
			Kind:   obj.Kind,
			Offset: int64(data.Len()),
			Length: int64(len(obj.Content)),
		}
		header.Objects = append(header.Objects, entry)
		data.Write(obj.Content)
	}

	// Serialize header
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshaling header: %w", err)
	}

	// Build pack
	var pack bytes.Buffer

	// Write header length
	headerLen := make([]byte, HeaderLengthSize)
	binary.BigEndian.PutUint32(headerLen, uint32(len(headerJSON)))
	pack.Write(headerLen)

	// Write header
	pack.Write(headerJSON)

	// Write data
	pack.Write(data.Bytes())

	// Compress
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		return nil, fmt.Errorf("creating zstd encoder: %w", err)
	}
	if _, err := encoder.Write(pack.Bytes()); err != nil {
		encoder.Close()
		return nil, fmt.Errorf("compressing: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("closing encoder: %w", err)
	}

	return compressed.Bytes(), nil
}

// PackObject represents an object to be packed.
type PackObject struct {
	Digest  []byte
	Kind    string
	Content []byte
}

// isSemanticKind returns true if the kind represents a semantic node.
func isSemanticKind(kind string) bool {
	switch kind {
	case "Snapshot", "ChangeSet", "Symbol", "Module", "File", "ChangeType", "Workspace":
		return true
	default:
		return false
	}
}

// ExtractObject extracts a single object from a segment.
func ExtractObject(db *store.DB, digest []byte) ([]byte, string, error) {
	info, err := db.GetObject(digest)
	if err != nil {
		return nil, "", err
	}

	blob, err := db.GetSegmentBlob(info.SegmentID)
	if err != nil {
		return nil, "", err
	}

	if info.Off+info.Len > int64(len(blob)) {
		return nil, "", fmt.Errorf("object extends beyond segment")
	}

	content := blob[info.Off : info.Off+info.Len]
	return content, info.Kind, nil
}

// ============================================================================
// Standalone functions for multi-repo support
// These functions take *sql.DB as a parameter instead of *store.DB.
// ============================================================================

// IngestSegmentToDB ingests a zstd-compressed pack from a reader using *sql.DB.
func IngestSegmentToDB(db *sql.DB, r io.Reader, actor string) (segmentID int64, indexed int, err error) {
	// Decompress the entire stream
	decoder, err := zstd.NewReader(r)
	if err != nil {
		return 0, 0, fmt.Errorf("creating zstd decoder: %w", err)
	}
	defer decoder.Close()

	// Read all decompressed content
	decompressed, err := io.ReadAll(decoder)
	if err != nil {
		return 0, 0, fmt.Errorf("decompressing: %w", err)
	}

	if len(decompressed) < HeaderLengthSize {
		return 0, 0, fmt.Errorf("pack too small: %d bytes", len(decompressed))
	}

	// Parse header length
	headerLen := binary.BigEndian.Uint32(decompressed[:HeaderLengthSize])
	if headerLen > MaxHeaderSize {
		return 0, 0, fmt.Errorf("header too large: %d bytes", headerLen)
	}
	if int(HeaderLengthSize+headerLen) > len(decompressed) {
		return 0, 0, fmt.Errorf("header length exceeds pack size")
	}

	// Parse header JSON
	headerData := decompressed[HeaderLengthSize : HeaderLengthSize+headerLen]
	var header proto.PackHeader
	if err := json.Unmarshal(headerData, &header); err != nil {
		return 0, 0, fmt.Errorf("parsing header: %w", err)
	}

	// Data starts after header
	dataStart := int64(HeaderLengthSize + headerLen)
	objectData := decompressed[dataStart:]

	// Compute checksum of the entire decompressed content
	checksum := cas.Blake3Hash(decompressed)

	// Begin transaction
	tx, err := db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert segment (store only the object data portion, not the header)
	segmentID, err = store.InsertSegmentTx(tx, checksum, objectData)
	if err != nil {
		return 0, 0, fmt.Errorf("inserting segment: %w", err)
	}

	// Index each object
	for _, obj := range header.Objects {
		if obj.Offset+obj.Length > int64(len(objectData)) {
			return 0, 0, fmt.Errorf("object %x extends beyond data", obj.Digest[:8])
		}

		// Verify digest
		content := objectData[obj.Offset : obj.Offset+obj.Length]
		computedDigest := cas.Blake3Hash(content)
		if !bytes.Equal(computedDigest, obj.Digest) {
			return 0, 0, fmt.Errorf("digest mismatch for object at offset %d", obj.Offset)
		}

		err = store.InsertObjectTx(tx, obj.Digest, segmentID, obj.Offset, obj.Length, obj.Kind)
		if err != nil {
			return 0, 0, fmt.Errorf("inserting object: %w", err)
		}

		// Enqueue snapshots for enrichment
		if obj.Kind == "Snapshot" {
			if err := store.EnqueueForEnrichmentTx(tx, obj.Digest, obj.Kind); err != nil {
				return 0, 0, fmt.Errorf("enqueueing for enrichment: %w", err)
			}
		}

		indexed++
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("committing transaction: %w", err)
	}

	return segmentID, indexed, nil
}

// ExtractObjectFromDB extracts a single object from a segment using *sql.DB.
func ExtractObjectFromDB(db *sql.DB, digest []byte) ([]byte, string, error) {
	info, err := store.GetObjectInfo(db, digest)
	if err != nil {
		return nil, "", err
	}

	blob, err := store.GetSegmentBlobByID(db, info.SegmentID)
	if err != nil {
		return nil, "", err
	}

	if info.Off+info.Len > int64(len(blob)) {
		return nil, "", fmt.Errorf("object extends beyond segment")
	}

	content := blob[info.Off : info.Off+info.Len]
	return content, info.Kind, nil
}
