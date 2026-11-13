package session

import (
	"fmt"
	"strings"
)

// ValidateStorageID rejects identifiers that escape a session's storage directory.
func ValidateStorageID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`+"\x00") {
		return fmt.Errorf("invalid session or attachment ID %q", id)
	}
	return nil
}
