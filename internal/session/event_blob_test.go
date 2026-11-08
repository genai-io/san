package session

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestBlobStoreRoundTripAndTamper(t *testing.T) {
	b := blobStore{dir: filepath.Join(t.TempDir(), "blobs")}
	req := StandaloneRequest{System: "review", Messages: []ai.Message{eventText("m", ai.RoleUser, "tool call under review")}, Tools: []ai.Schema{{Name: "Read"}}}
	digest, err := b.PutRequest(req)
	if err != nil || !validBlobDigest(digest) {
		t.Fatalf("digest %q: %v", digest, err)
	}
	again, err := b.PutRequest(req)
	if err != nil || again != digest {
		t.Fatal("same request must have the same digest and not fail on rewrite")
	}
	got, err := b.Request(digest)
	if err != nil || !reflect.DeepEqual(got, req) {
		t.Fatalf("round trip: %v", err)
	}
	// An event referencing a missing blob must be detectable.
	if _, err := b.Get(blobDigest([]byte("absent"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing blob: %v", err)
	}
	if err := os.WriteFile(filepath.Join(b.dir, digest), []byte(`{"messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(digest); !errors.Is(err, ErrBlobCorrupt) {
		t.Fatalf("tampered blob accepted: %v", err)
	}
	// A damaged blob is replaced by a fresh Put of the true content.
	if _, err := b.PutRequest(req); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(digest); err != nil {
		t.Fatalf("not repaired: %v", err)
	}
	for _, bad := range []string{"", "../x", "ABC", digest[:10], digest + "00"} {
		if _, err := b.Get(bad); err == nil {
			t.Fatalf("accepted digest %q", bad)
		}
	}
}
