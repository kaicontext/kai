package sshserver

import (
	"bufio"
	"bytes"
	"testing"
)

func TestReadReceivePackRequest(t *testing.T) {
	var buf bytes.Buffer
	if err := writePktLine(&buf, "0000000000000000000000000000000000000000 "+
		"1111111111111111111111111111111111111111 refs/heads/main\x00report-status\n"); err != nil {
		t.Fatalf("write pkt: %v", err)
	}
	if err := writePktLine(&buf, "1111111111111111111111111111111111111111 "+
		"2222222222222222222222222222222222222222 refs/heads/dev\n"); err != nil {
		t.Fatalf("write pkt: %v", err)
	}
	if err := writeFlush(&buf); err != nil {
		t.Fatalf("write flush: %v", err)
	}
	buf.WriteString("PACKDATA")
	reader := bufio.NewReader(&buf)

	req, err := readReceivePackRequest(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(req.Updates))
	}
	if req.Updates[0].Ref != "refs/heads/main" {
		t.Fatalf("unexpected ref: %s", req.Updates[0].Ref)
	}
	if string(req.Pack) != "PACKDATA" {
		t.Fatalf("unexpected pack: %q", string(req.Pack))
	}
}
