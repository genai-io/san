package autoupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
)

const sumsFixture = "aaaa  san_darwin_arm64.tar.gz\nbbbb  san_linux_amd64.tar.gz\n"

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestVerifyChecksums(t *testing.T) {
	pub, priv := keypair(t)
	got, err := VerifyChecksums(pub, []byte(sumsFixture), Sign(priv, []byte(sumsFixture)))
	if err != nil {
		t.Fatalf("VerifyChecksums() error: %v", err)
	}
	if got["san_darwin_arm64.tar.gz"] != "aaaa" || got["san_linux_amd64.tar.gz"] != "bbbb" {
		t.Errorf("checksums = %v", got)
	}
}

func TestVerifyChecksumsRefuses(t *testing.T) {
	pub, priv := keypair(t)
	_, otherPriv := keypair(t)
	good := Sign(priv, []byte(sumsFixture))

	cases := map[string]struct {
		pub  ed25519.PublicKey
		sums string
		sig  []byte
	}{
		"tampered sums":   {pub, sumsFixture + "cccc  san_evil.tar.gz\n", good},
		"foreign key":     {pub, sumsFixture, Sign(otherPriv, []byte(sumsFixture))},
		"garbage sig":     {pub, sumsFixture, []byte("not base64!")},
		"truncated sig":   {pub, sumsFixture, good[:20]},
		"empty signature": {pub, sumsFixture, nil},
		"no built-in key": {nil, sumsFixture, good},
	}
	for name, tc := range cases {
		if _, err := VerifyChecksums(tc.pub, []byte(tc.sums), tc.sig); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// serveRelease serves the given files as release v1.0.0's assets.
func serveRelease(t *testing.T, files map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[path.Base(r.URL.Path)]
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
	pub, priv := keypair(t)
	serveRelease(t, map[string]string{
		"SHA256SUMS":     sumsFixture,
		"SHA256SUMS.sig": string(Sign(priv, []byte(sumsFixture))),
	})
	got, err := fetchChecksums(context.Background(), "1.0.0", pub)
	if err != nil {
		t.Fatalf("fetchChecksums() error: %v", err)
	}
	if got["san_linux_amd64.tar.gz"] != "bbbb" {
		t.Errorf("checksums = %v", got)
	}
}

func TestFetchChecksumsRejectsAnUnsignedRelease(t *testing.T) {
	pub, _ := keypair(t)
	serveRelease(t, map[string]string{"SHA256SUMS": sumsFixture}) // no .sig
	if _, err := fetchChecksums(context.Background(), "1.0.0", pub); err == nil {
		t.Fatal("a release without SHA256SUMS.sig must be refused")
	}
	serveRelease(t, map[string]string{}) // nothing at all
	if _, err := fetchChecksums(context.Background(), "1.0.0", pub); err == nil {
		t.Fatal("a release without SHA256SUMS must be refused")
	}
}
