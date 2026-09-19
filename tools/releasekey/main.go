// Command releasekey creates and uses the release signing key that
// internal/autoupdate verifies releases against.
//
//	go run ./tools/releasekey gen <keyfile>   write a new private key to keyfile (mode 0600) and print its public key
//	go run ./tools/releasekey sign <file>     write <file>.sig, signed with $SAN_RELEASE_SIGNING_KEY
//
// sign refuses a signature the key built into san would not accept, so a
// secret that does not match the code fails the release instead of shipping
// one no client will install. Setup and rotation: docs/operations/release.md.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/genai-io/san/internal/autoupdate"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: releasekey gen <keyfile> | sign <file>")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = gen(os.Args[2])
	case "sign":
		err = signFile(os.Args[2])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasekey:", err)
		os.Exit(1)
	}
}

func gen(keyfile string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyfile, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

func signFile(path string) error {
	priv, err := base64.StdEncoding.DecodeString(os.Getenv("SAN_RELEASE_SIGNING_KEY"))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("SAN_RELEASE_SIGNING_KEY is not a base64 ed25519 private key")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig := autoupdate.Sign(priv, data)
	if _, err := autoupdate.VerifyChecksums(autoupdate.ReleasePublicKey(), data, sig); err != nil {
		return err
	}
	return os.WriteFile(path+".sig", sig, 0o644)
}
