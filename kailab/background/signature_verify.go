package background

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"kai-core/cas"
)

// VerifyChangeSetSignature checks if a ChangeSet signature is valid.
func VerifyChangeSetSignature(payload map[string]interface{}) (bool, error) {
	sigBlob, _ := payload["signature"].(string)
	sigFormat, _ := payload["sigFormat"].(string)
	signer, _ := payload["signer"].(string)
	sigType, _ := payload["sigType"].(string)
	if sigBlob == "" || sigFormat == "" || sigType != "ssh" {
		return false, nil
	}

	keys, err := loadTrustedKeysFromEnv()
	if err != nil {
		return false, err
	}
	if len(keys) == 0 {
		return false, nil
	}

	unsigned := cloneWithoutSignature(payload)
	data, err := cas.CanonicalJSON(unsigned)
	if err != nil {
		return false, fmt.Errorf("canonical json: %w", err)
	}

	rawSig, err := base64.StdEncoding.DecodeString(sigBlob)
	if err != nil {
		return false, fmt.Errorf("decode signature: %w", err)
	}
	sig := &ssh.Signature{
		Format: sigFormat,
		Blob:   rawSig,
	}

	for _, key := range keys {
		if signer != "" && ssh.FingerprintSHA256(key) != signer {
			continue
		}
		if err := key.Verify(data, sig); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// ParseObjectPayload parses a node object payload.
func ParseObjectPayload(content []byte) (map[string]interface{}, error) {
	payload := content
	if idx := bytes.IndexByte(content, '\n'); idx >= 0 {
		payload = content[idx+1:]
	}
	var out map[string]interface{}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func cloneWithoutSignature(payload map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(payload))
	for k, v := range payload {
		switch k {
		case "signature", "sigFormat", "sigType", "signer", "signedAt":
			continue
		default:
			out[k] = v
		}
	}
	return out
}

func loadTrustedKeysFromEnv() ([]ssh.PublicKey, error) {
	files := splitList(os.Getenv("KAILAB_SSH_SIGN_KEYS"))
	if len(files) == 0 {
		return nil, nil
	}
	var keys []ssh.PublicKey
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read keys: %w", err)
		}
		for len(data) > 0 {
			key, _, _, rest, err := ssh.ParseAuthorizedKey(data)
			if err != nil {
				break
			}
			if key != nil {
				keys = append(keys, key)
			}
			data = rest
		}
	}
	return keys, nil
}

func splitList(val string) []string {
	if val == "" {
		return nil
	}
	parts := strings.FieldsFunc(val, func(r rune) bool {
		return r == ',' || r == ';'
	})
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
