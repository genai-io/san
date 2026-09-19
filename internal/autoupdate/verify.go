package autoupdate

// Every release ships SHA256SUMS — one "<hex>  <asset>" line per archive —
// and SHA256SUMS.sig, the ed25519 signature the release workflow makes over
// that file with the key it holds as SAN_RELEASE_SIGNING_KEY (see
// tools/releasesign). A client trusts an archive only when the signature
// verifies against the public key built into this binary and the archive
// hashes to the listed sum. TLS to github.com proves who served the bytes;
// the signature proves who published them.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// releasePublicKeyBase64 is the public half of the release signing key.
// Rotating it means every client built with the old key refuses updates until
// it is reinstalled — the safe direction to fail in.
const releasePublicKeyBase64 = ""

// releasePublicKey is releasePublicKeyBase64 decoded; a var so tests can sign
// with a key of their own.
var releasePublicKey, _ = base64.StdEncoding.DecodeString(releasePublicKeyBase64)

// VerifyChecksums checks sig over sums against the built-in release key and
// returns the listed checksums keyed by asset name.
func VerifyChecksums(sums, sig []byte) (map[string]string, error) {
	if len(releasePublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("no release signing key built into this binary")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(releasePublicKey), sums, raw) {
		return nil, errors.New("SHA256SUMS signature does not verify against the built-in release key")
	}
	out := map[string]string{}
	for line := range strings.SplitSeq(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			out[f[1]] = f[0]
		}
	}
	return out, nil
}

// fetchChecksums downloads and verifies release version's checksum list.
func fetchChecksums(ctx context.Context, version string) (map[string]string, error) {
	sums, err := fetchSmall(ctx, fmt.Sprintf(downloadURL, version, "SHA256SUMS"))
	if err != nil {
		return nil, fmt.Errorf("release v%s has no signed checksums: %w", version, err)
	}
	sig, err := fetchSmall(ctx, fmt.Sprintf(downloadURL, version, "SHA256SUMS.sig"))
	if err != nil {
		return nil, fmt.Errorf("release v%s has no signed checksums: %w", version, err)
	}
	return VerifyChecksums(sums, sig)
}

// fetchSmall reads a release metadata file into memory, capped well above
// the few hundred bytes it should be.
func fetchSmall(ctx context.Context, url string) ([]byte, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}
