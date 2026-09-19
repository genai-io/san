package autoupdate

// A release ships SHA256SUMS — one "<hex>  <asset>" line per archive — and
// SHA256SUMS.sig, the base64 ed25519 signature over that file that
// tools/releasekey makes with the project's release key. A client installs an
// archive only when the signature verifies against the public key built into
// this binary and the archive hashes to the listed sum. Key setup and
// rotation: docs/operations/release.md.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// releasePublicKeyBase64 is the public half of the release signing key, as
// printed by `releasekey gen`. Empty until a maintainer sets one up; until
// then every verification fails and the release workflow refuses to publish.
const releasePublicKeyBase64 = ""

// ReleasePublicKey is the release signing key built into this binary.
func ReleasePublicKey() ed25519.PublicKey {
	key, _ := base64.StdEncoding.DecodeString(releasePublicKeyBase64)
	return key
}

// Sign returns the SHA256SUMS.sig contents for data.
func Sign(priv ed25519.PrivateKey, data []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)) + "\n")
}

// VerifyChecksums checks sig over sums against pub and returns the listed
// checksums keyed by asset name.
func VerifyChecksums(pub ed25519.PublicKey, sums, sig []byte) (map[string]string, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("no release signing key built into this binary")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(pub, sums, raw) {
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

// fetchChecksums downloads release version's checksum list and verifies it
// against pub.
func fetchChecksums(ctx context.Context, version string, pub ed25519.PublicKey) (map[string]string, error) {
	sums, err := fetchAsset(ctx, version, "SHA256SUMS")
	if err != nil {
		return nil, err
	}
	sig, err := fetchAsset(ctx, version, "SHA256SUMS.sig")
	if err != nil {
		return nil, err
	}
	return VerifyChecksums(pub, sums, sig)
}

// fetchAsset reads a small release asset into memory, capped well above the
// few hundred bytes a checksum list or signature is.
func fetchAsset(ctx context.Context, version, name string) ([]byte, error) {
	resp, err := get(ctx, fmt.Sprintf(downloadURL, version, name))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}
