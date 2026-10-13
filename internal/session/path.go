package session

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// ProjectID keeps storage paths bounded regardless of the working directory's length.
func ProjectID(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return hex.EncodeToString(sum[:12])
}
