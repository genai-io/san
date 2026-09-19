# Release

Release automation is currently driven by `Makefile`.

## Commands

```bash
make release
make release-push VERSION=vX.Y.Z
```

`release-push` expects a clean worktree and a matching `CHANGELOG.md` section.

## Signing

`make release` writes `bin/SHA256SUMS` next to the archives. The release
workflow signs it with the ed25519 key in the `SAN_RELEASE_SIGNING_KEY`
repository secret and publishes `SHA256SUMS` and `SHA256SUMS.sig` with the
release. `san update` and the background auto-update refuse an archive unless
the signature verifies against the public key built into the binary
(`releasePublicKeyBase64` in `internal/autoupdate/verify.go`) and the archive
hashes to the listed sum.

The workflow verifies its own signature against the built-in key before
publishing, so a secret that does not match the code fails the release.

One-time setup, or rotation:

```bash
go run ./tools/releasesign gen ~/san-release.key   # prints the public key
gh secret set SAN_RELEASE_SIGNING_KEY --repo genai-io/san < ~/san-release.key
```

Keep the keyfile in a password manager: a client verifies with the key it
was built with, so after a rotation every client built with the old key
refuses updates until it is reinstalled — losing the key means exactly that
for everyone.
