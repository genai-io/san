---
layer: feature
---

# upgrade

Replaces the running `san` binary with a newer GitHub release. The running
process is never touched: the release is downloaded and extracted beside the
binary, then swapped in by rename, so the session keeps the version it started
with and the next launch runs the new one.

```go
package upgrade

func InstallDir() string
func Managed() bool
func Latest(ctx context.Context) (string, error)
func Newer(a, b string) bool
func IsRelease(v string) bool
func Install(ctx context.Context, version string, progress func(written, total int64)) error
func ReleasePublicKey() ed25519.PublicKey
func Sign(priv ed25519.PrivateKey, data []byte) []byte
func VerifyChecksums(pub ed25519.PublicKey, sums, sig []byte) (map[string]string, error)
func Cleanup()
```

- `san update` calls it interactively, wherever the binary lives. The TUI
  calls it once per launch on a background goroutine — the startup path pays
  nothing — and only when `Managed()` says the binary is in `InstallDir()`
  (`~/.local/bin`, or `%LOCALAPPDATA%\san\bin` on Windows), so a Homebrew,
  `go install`, package-manager, or dev-build binary is never replaced behind
  its owner's back. Success shows `vX.Y.Z installed · Restart to update` right-aligned
  above the input, sharing the task-status row without displacing it. A failed
  install warns at exit with `san update`; an offline check is silent. The session
  itself is never touched. `SAN_DISABLE_AUTOUPDATE=1` turns the check off;
  `san update` still works.
- `Newer` only accepts plain `X.Y.Z`, so a dev build (`git describe` suffix,
  bare hash) is never replaced and a release never downgrades; `IsRelease`
  lets the caller skip the network for one. `Install` extracts only the
  archive entry named `san`/`san.exe`, beside the binary, and fails before any
  network traffic when that directory is not writable.
- Nothing is installed that the project did not publish: `Install` first
  fetches the release's `SHA256SUMS` and `SHA256SUMS.sig`, verifies the
  ed25519 signature against the public key built into the binary
  (`verify.go`), then hashes the archive as it downloads and refuses a
  mismatch. TLS proves who served the bytes; the signature proves who
  published them. Key setup and rotation: [`../operations/release.md`](../operations/release.md).
- On POSIX the swap is one atomic rename, so the path always names a whole
  binary; Windows cannot replace a running executable, so there the old one is
  moved aside first. `Cleanup`, run in the background at every TUI launch,
  removes that moved-aside binary and download directories abandoned by a
  session that quit mid-install.
- Code: `internal/upgrade/`.
