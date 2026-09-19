// Command releasekey creates and uses the release signing key that
// internal/autoupdate verifies releases against.
//
//	go run ./tools/releasekey gen <keyfile>   write a new private key to keyfile (mode 0600) and print its public key
//	go run ./tools/releasekey sign <file>     write <file>.sig, signed with $SAN_RELEASE_SIGNING_KEY
//	go run ./tools/releasekey verify <file>   check <file>.sig against the key built into san
//
// One-time setup: run gen, keep the keyfile somewhere durable (a password
// manager — losing it means every shipped client refuses further updates
// until reinstalled), store it as the SAN_RELEASE_SIGNING_KEY repository
// secret, and paste the printed public key into releasePublicKeyBase64 in
// internal/autoupdate/verify.go. The release workflow then signs SHA256SUMS
// and verifies its own signature before publishing, so a secret that does not
// match the built-in key fails the release instead of shipping.
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
		fmt.Fprintln(os.Stderr, "usage: releasekey gen <keyfile> | sign <file> | verify <file>")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = gen(os.Args[2])
	case "sign":
		err = signFile(os.Args[2])
	case "verify":
		err = verify(os.Args[2])
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
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(priv), data))
	return os.WriteFile(path+".sig", []byte(sig+"\n"), 0o644)
}

func verify(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(path + ".sig")
	if err != nil {
		return err
	}
	sums, err := autoupdate.VerifyChecksums(data, sig)
	if err != nil {
		return err
	}
	fmt.Printf("ok: %s signed for %d assets\n", path, len(sums))
	return nil
}
