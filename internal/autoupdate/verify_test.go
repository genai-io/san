package autoupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReleasePublicKeyIsBuiltIn is the gate that keeps an unsigned build from
// shipping: with no key every update is refused, so the constant must be set
// before this package can be released.
func TestReleasePublicKeyIsBuiltIn(t *testing.T) {
	if len(releasePublicKey) != ed25519.PublicKeySize {
		t.Fatalf("releasePublicKeyBase64 must hold a 32-byte ed25519 public key; run `go run ./tools/releasesign gen` and paste the printed key")
	}
}

// signingKey installs a fresh keypair as the built-in release key for the
// test and returns the private half for signing.
func signingKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := releasePublicKey
	releasePublicKey = pub
	t.Cleanup(func() { releasePublicKey = old })
	return priv
}

func sign(priv ed25519.PrivateKey, sums string) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums))) + "\n")
}

const sumsFixture = "aaaa  san_darwin_arm64.tar.gz\nbbbb  san_linux_amd64.tar.gz\n"

func TestVerifyChecksums(t *testing.T) {
	priv := signingKey(t)
	got, err := VerifyChecksums([]byte(sumsFixture), sign(priv, sumsFixture))
	if err != nil {
		t.Fatalf("VerifyChecksums() error: %v", err)
	}
	if got["san_darwin_arm64.tar.gz"] != "aaaa" || got["san_linux_amd64.tar.gz"] != "bbbb" {
		t.Errorf("checksums = %v", got)
	}
}

func TestVerifyChecksumsRefuses(t *testing.T) {
	priv := signingKey(t)
	good := sign(priv, sumsFixture)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)

	cases := map[string]struct {
		sums string
		sig  []byte
	}{
		"tampered sums":   {sumsFixture + "cccc  san_evil.tar.gz\n", good},
		"foreign key":     {sumsFixture, sign(otherPriv, sumsFixture)},
		"garbage sig":     {sumsFixture, []byte("not base64!")},
		"truncated sig":   {sumsFixture, good[:20]},
		"empty signature": {sumsFixture, nil},
	}
	for name, tc := range cases {
		if _, err := VerifyChecksums([]byte(tc.sums), tc.sig); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestVerifyChecksumsRequiresABuiltInKey(t *testing.T) {
	priv := signingKey(t)
	sig := sign(priv, sumsFixture)
	releasePublicKey = nil
	if _, err := VerifyChecksums([]byte(sumsFixture), sig); err == nil {
		t.Fatal("an empty built-in key must refuse every signature")
	}
}

// serveRelease serves the given files as release v1.0.0's assets.
func serveRelease(t *testing.T, files map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		body, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	old := downloadURL
	downloadURL = srv.URL + "/v%s/%s"
	t.Cleanup(func() { downloadURL = old })
}

func TestFetchChecksums(t *testing.T) {
	priv := signingKey(t)
	serveRelease(t, map[string]string{
		"SHA256SUMS":     sumsFixture,
		"SHA256SUMS.sig": string(sign(priv, sumsFixture)),
	})
	got, err := fetchChecksums(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("fetchChecksums() error: %v", err)
	}
	if got["san_linux_amd64.tar.gz"] != "bbbb" {
		t.Errorf("checksums = %v", got)
	}
}

func TestFetchChecksumsRejectsAnUnsignedRelease(t *testing.T) {
	signingKey(t)
	serveRelease(t, map[string]string{"SHA256SUMS": sumsFixture}) // no .sig
	if _, err := fetchChecksums(context.Background(), "1.0.0"); err == nil {
		t.Fatal("a release without SHA256SUMS.sig must be refused")
	}
	serveRelease(t, map[string]string{}) // nothing at all
	if _, err := fetchChecksums(context.Background(), "1.0.0"); err == nil {
		t.Fatal("a release without SHA256SUMS must be refused")
	}
}
