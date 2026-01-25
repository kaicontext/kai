package sshserver

import (
	"bufio"
	"crypto/sha1"
	"fmt"
	"io"
	"strconv"
)

// writePktLine writes a git pkt-line with the provided payload.
func writePktLine(w io.Writer, payload string) error {
	if w == nil {
		return fmt.Errorf("nil writer")
	}

	size := len(payload) + 4
	if size > 0xffff {
		return fmt.Errorf("pkt-line too long: %d", size)
	}

	header := fmt.Sprintf("%04x", size)
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := io.WriteString(w, payload)
	return err
}

// writeFlush writes a pkt-line flush (0000).
func writeFlush(w io.Writer) error {
	if w == nil {
		return fmt.Errorf("nil writer")
	}
	_, err := io.WriteString(w, "0000")
	return err
}

func writeGitError(w io.Writer, msg string) error {
	return writePktLine(w, "ERR "+msg)
}

// readPktLine reads a single pkt-line. Returns payload (without length), and flush flag.
func readPktLine(r *bufio.Reader) (string, bool, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return "", false, err
	}

	n, err := strconv.ParseInt(string(header), 16, 0)
	if err != nil {
		return "", false, fmt.Errorf("invalid pkt-line header: %w", err)
	}
	if n == 0 {
		return "", true, nil
	}
	if n < 4 {
		return "", false, fmt.Errorf("invalid pkt-line length: %d", n)
	}

	payloadLen := int(n) - 4
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", false, err
	}
	return string(payload), false, nil
}

// writeEmptyPack writes a valid empty packfile (version 2, 0 objects).
func writeEmptyPack(w io.Writer) error {
	header := []byte{'P', 'A', 'C', 'K', 0, 0, 0, 2, 0, 0, 0, 0}
	h := sha1.Sum(header)
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(h[:])
	return err
}
