package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// StandaloneRequest is the exact copy of a model request that cannot be rebuilt
// from the session's events: the system prompt, messages and tools as sent.
// `inference.start {input.standalone}` holds its digest.
type StandaloneRequest struct {
	System   string       `json:"system,omitempty"`
	Messages []ai.Message `json:"messages"`
	Tools    []ai.Schema  `json:"tools,omitempty"`
}

// ErrBlobCorrupt means stored bytes no longer match their digest.
var ErrBlobCorrupt = errors.New("session: blob does not match its digest")

// blobStore keeps content-addressed files in one session's blobs directory. A
// digest names exactly one content, so a blob is written once, before the event
// that references it, and never changes.
type blobStore struct{ dir string }

func blobDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validBlobDigest(digest string) bool {
	b, err := hex.DecodeString(digest)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == digest
}

func (b blobStore) path(digest string) (string, error) {
	if !validBlobDigest(digest) {
		return "", fmt.Errorf("invalid blob digest %q", digest)
	}
	return filepath.Join(b.dir, digest), nil
}

// Put stores data and returns its digest. An existing identical blob is kept.
func (b blobStore) Put(data []byte) (string, error) {
	digest := blobDigest(data)
	path, _ := b.path(digest)
	if old, err := os.ReadFile(path); err != nil || blobDigest(old) != digest {
		if err := atomicfile.Write(path, data, 0o600); err != nil {
			return "", err
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return "", err
	}
	err = f.Sync()
	return digest, errors.Join(err, f.Close())
}

// Get returns the content for digest, verifying it. A missing blob is
// os.ErrNotExist; damaged bytes are ErrBlobCorrupt, never returned.
func (b blobStore) Get(digest string) ([]byte, error) {
	path, err := b.path(digest)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if blobDigest(data) != digest {
		return nil, ErrBlobCorrupt
	}
	return data, nil
}

// PutRequest stores req and returns the digest for InferenceInput.Standalone.
func (b blobStore) PutRequest(req StandaloneRequest) (string, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return b.Put(data)
}

// Request loads and decodes a stored standalone request.
func (b blobStore) Request(digest string) (StandaloneRequest, error) {
	var req StandaloneRequest
	data, err := b.Get(digest)
	if err != nil {
		return req, err
	}
	return req, strictEventJSON(data, &req)
}
