package sshserver

import (
	"bufio"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"kai-core/cas"
	"kailab/store"
)

type receivePackUpdate struct {
	Old string
	New string
	Ref string
}

type receivePackRequest struct {
	Updates []receivePackUpdate
	Pack    []byte
}

func handleReceivePack(db *sql.DB, r io.Reader, w io.Writer) error {
	reader := bufio.NewReader(r)
	req, err := readReceivePackRequest(reader)
	if err != nil {
		return err
	}

	if db == nil {
		return fmt.Errorf("repository not available")
	}

	csDigest, err := createChangeSetFromPack(db, req)
	if err != nil {
		return err
	}

	return writeReceivePackStatus(w, req, csDigest)
}

func readReceivePackRequest(r *bufio.Reader) (*receivePackRequest, error) {
	req := &receivePackRequest{}

	for {
		line, flush, err := readPktLine(r)
		if err != nil {
			return nil, err
		}
		if flush {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.IndexByte(line, 0); idx >= 0 {
			line = line[:idx]
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("invalid receive-pack line: %q", line)
		}
		req.Updates = append(req.Updates, receivePackUpdate{
			Old: fields[0],
			New: fields[1],
			Ref: fields[2],
		})
	}

	packBytes, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	req.Pack = packBytes
	return req, nil
}

func createChangeSetFromPack(db *sql.DB, req *receivePackRequest) ([]byte, error) {
	baseDigest, err := resolveBaseSnapshot(db)
	if err != nil {
		return nil, err
	}

	packDigestHex := ""
	if len(req.Pack) > 0 {
		packDigest, err := storeRawObject(db, "GitPack", req.Pack)
		if err != nil {
			return nil, err
		}
		packDigestHex = hex.EncodeToString(packDigest)
	}

	payload := map[string]interface{}{
		"base":      hex.EncodeToString(baseDigest),
		"head":      hex.EncodeToString(baseDigest),
		"createdAt": cas.NowMs(),
	}
	if packDigestHex != "" {
		payload["gitPack"] = packDigestHex
	}
	csDigest, err := storeNodeObject(db, "ChangeSet", payload)
	if err != nil {
		return nil, err
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := store.ForceSetRef(db, tx, "cs.latest", csDigest, "ssh", "git-receive-pack"); err != nil {
		return nil, err
	}
	return csDigest, tx.Commit()
}

func resolveBaseSnapshot(db *sql.DB) ([]byte, error) {
	ref, err := store.GetRef(db, "snap.main")
	if err == nil && ref != nil {
		return ref.Target, nil
	}
	return storeNodeObject(db, "Snapshot", map[string]interface{}{
		"sourceType":  "git",
		"sourceRef":   "receive-pack",
		"fileCount":   0,
		"fileDigests": []string{},
		"files":       []interface{}{},
		"createdAt":   cas.NowMs(),
	})
}

func storeRawObject(db *sql.DB, kind string, payload []byte) ([]byte, error) {
	content := append([]byte(kind+"\n"), payload...)
	digest := computeBlake3(content)
	checksum := make([]byte, 32)
	if _, err := rand.Read(checksum); err != nil {
		return nil, err
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	segmentID, err := store.InsertSegmentTx(tx, checksum, content)
	if err != nil {
		return nil, err
	}
	if err := store.InsertObjectTx(tx, digest, segmentID, 0, int64(len(content)), kind); err != nil {
		return nil, err
	}
	return digest, tx.Commit()
}

func storeNodeObject(db *sql.DB, kind string, payload interface{}) ([]byte, error) {
	digest, err := cas.NodeID(kind, payload)
	if err != nil {
		return nil, err
	}
	payloadJSON, err := cas.CanonicalJSON(payload)
	if err != nil {
		return nil, err
	}
	content := append([]byte(kind+"\n"), payloadJSON...)

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	checksum := computeBlake3(content)
	segmentID, err := store.InsertSegmentTx(tx, checksum, content)
	if err != nil {
		return nil, err
	}
	if err := store.InsertObjectTx(tx, digest, segmentID, 0, int64(len(content)), kind); err != nil {
		return nil, err
	}
	return digest, tx.Commit()
}

func writeReceivePackStatus(w io.Writer, req *receivePackRequest, csDigest []byte) error {
	if err := writePktLine(w, "unpack ok\n"); err != nil {
		return err
	}
	for _, update := range req.Updates {
		if err := writePktLine(w, "ok "+update.Ref+"\n"); err != nil {
			return err
		}
	}
	_ = csDigest
	return writeFlush(w)
}

func computeBlake3(data []byte) []byte {
	h := cas.NewBlake3Hasher()
	h.Write(data)
	return h.Sum(nil)
}
