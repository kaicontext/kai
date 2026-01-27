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

func writePktLineBytes(w io.Writer, payload []byte) error {
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
	_, err := w.Write(payload)
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

// writeDelim writes a pkt-line delimiter (0001) used in protocol v2.
func writeDelim(w io.Writer) error {
	if w == nil {
		return fmt.Errorf("nil writer")
	}
	_, err := io.WriteString(w, "0001")
	return err
}

func writeGitError(w io.Writer, msg string) error {
	return writePktLine(w, "ERR "+msg)
}

// PktType represents the type of pkt-line packet
type PktType int

const (
	PktData  PktType = iota // Normal data packet
	PktFlush                // 0000 - flush packet
	PktDelim                // 0001 - delimiter packet (v2)
)

// readPktLine reads a single pkt-line. Returns payload (without length), and flush flag.
// Note: This treats both delimiter (0001) and flush (0000) as flush=true for v1 compatibility.
func readPktLine(r *bufio.Reader) (string, bool, error) {
	payload, pktType, err := readPktLineV2(r)
	if err != nil {
		return "", false, err
	}
	return payload, pktType == PktFlush || pktType == PktDelim, nil
}

// readPktLineV2 reads a pkt-line and distinguishes between flush and delimiter.
func readPktLineV2(r *bufio.Reader) (string, PktType, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return "", PktData, err
	}

	if string(header) == "0001" {
		return "", PktDelim, nil
	}

	n, err := strconv.ParseInt(string(header), 16, 0)
	if err != nil {
		return "", PktData, fmt.Errorf("invalid pkt-line header: %w", err)
	}
	if n == 0 {
		return "", PktFlush, nil
	}
	if n < 4 {
		return "", PktData, fmt.Errorf("invalid pkt-line length: %d", n)
	}

	payloadLen := int(n) - 4
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", PktData, err
	}
	return string(payload), PktData, nil
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

type sideBandWriter struct {
	w         io.Writer
	maxData   int
	channelID byte
}

func (s *sideBandWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		chunk := len(p)
		if chunk > s.maxData {
			chunk = s.maxData
		}
		payload := make([]byte, chunk+1)
		payload[0] = s.channelID
		copy(payload[1:], p[:chunk])
		if err := writePktLineBytes(s.w, payload); err != nil {
			return written, err
		}
		written += chunk
		p = p[chunk:]
	}
	return written, nil
}
