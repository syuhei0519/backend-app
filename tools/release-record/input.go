// JSON and digest boundaries shared by the current SBOM verifier.
package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

var ErrRefused = errors.New("SBOM input refused")
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

const maxEvidenceBytes = 32 << 20

func Checksum(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func uniqueKeys(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var read func(int) error
	read = func(depth int) error {
		if depth > 64 {
			return ErrRefused
		}
		t, err := d.Token()
		if err != nil {
			return ErrRefused
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					s, ok := k.(string)
					if e != nil || !ok || keys[s] {
						return ErrRefused
					}
					keys[s] = true
					if read(depth+1) != nil {
						return ErrRefused
					}
				}
			case '[':
				for d.More() {
					if read(depth+1) != nil {
						return ErrRefused
					}
				}
			default:
				return ErrRefused
			}
			if _, err = d.Token(); err != nil {
				return ErrRefused
			}
		}
		return nil
	}
	if read(0) != nil {
		return ErrRefused
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrRefused
	}
	return nil
}
