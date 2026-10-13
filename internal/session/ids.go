package session

import (
	"crypto/rand"
	"encoding/hex"
)

func NewSessionID() string {
	var id [12]byte
	_, _ = rand.Read(id[:])
	return hex.EncodeToString(id[:])
}
