// Package autoupdate replaces the running san binary with a newer GitHub
// release. The running process is never touched: the new binary is downloaded
// beside the old one and swapped in by rename, so a session keeps running on
// the version it started with and the next launch picks up the new one.
//
// Two callers share it: `san update` (interactive, with a progress bar, works
// wherever the binary lives) and the TUI's background check at startup
// (silent, only for a binary in InstallDir, reports through the status line).
package autoupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Vars so tests can serve a release from a local server. latestURL redirects
// to the newest release tag; a HEAD request reads the version off the
// Location header without touching the rate-limited API. downloadURL takes
// the version and an asset name.
var (
	latestURL   = "https://github.com/genai-io/san/releases/latest"
	downloadURL = "https://github.com/genai-io/san/releases/download/v%s/%s"
)

const (
	tagPrefix = "https://github.com/genai-io/san/releases/tag/v"

	backupSuffix = ".bak"
	tempPrefix   = ".san-update-"
	// staleAfter is how old an abandoned download directory must be before
	// Cleanup removes it: a session that quits mid-download leaves one behind,
	// while a download in progress in another session must be left alone.
	staleAfter = time.Hour
)

// InstallDir is where install.sh / install.ps1 put the binary. It is the one
// location the background updater replaces: a binary anywhere else belongs to
// whatever put it there (Homebrew, `go install`, a package manager, a dev
// build), and only an explicit `san update` touches those.
func InstallDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "san", "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

// Managed reports whether the running binary lives in InstallDir.
func Managed() bool {
	exe, err := executable()
	if err != nil {
		return false
	}
	dir, err := filepath.EvalSymlinks(InstallDir())
	return err == nil && filepath.Dir(exe) == dir
}

// Latest returns the newest released version, without the "v".
func Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("unexpected response from GitHub: %s", resp.Status)
	}
	location := resp.Header.Get("Location")
	version := strings.TrimPrefix(location, tagPrefix)
	if version == "" || version == location {
		return "", fmt.Errorf("unexpected Location header: %q", location)
	}
	return version, nil
}

// Newer reports whether release a is strictly newer than b. Both must be plain
// "X.Y.Z" (a leading "v" is tolerated); anything else — a dev build's
// "v1.22.8-3-gabc1234-dirty", a bare commit hash, "dev" — compares false, so a
// build that is not a release is never replaced and a release never downgrades.
func Newer(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

// IsRelease reports whether v is a plain release version ("X.Y.Z", optional
// leading "v") rather than a dev build.
func IsRelease(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Install downloads release version and swaps it in for the running binary.
// progress, when non-nil, is called as download bytes arrive.
//
// Two installs racing (a background check in one session, `san update` in
// another) converge on the same binary: the second rename of the old file
// fails once the first has moved it, and a later full pass just re-installs
// the version already in place. Each pass uses its own temp dir, so no lock
// is needed — the cost of the race is one duplicate download per release.
func Install(ctx context.Context, version string, progress func(written, total int64)) error {
	exe, err := executable()
	if err != nil {
		return err
	}

	archiveExt, binName := ".tar.gz", "san"
	if runtime.GOOS == "windows" {
		archiveExt, binName = ".zip", "san.exe"
	}
	assetName := fmt.Sprintf("san_%s_%s%s", runtime.GOOS, runtime.GOARCH, archiveExt)

	// Download and extract beside the binary so the final rename stays on one
	// filesystem. This also fails fast, before any network traffic, when the
	// directory is not ours to write.
	tmpDir, err := os.MkdirTemp(filepath.Dir(exe), tempPrefix+"*")
	if err != nil {
		return fmt.Errorf("cannot create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// The signed checksum list comes first: a release that does not verify
	// costs nothing more than these two small fetches.
	sums, err := fetchChecksums(ctx, version)
	if err != nil {
		return err
	}
	want, ok := sums[assetName]
	if !ok {
		return fmt.Errorf("release v%s lists no checksum for %s", version, assetName)
	}

	archive := filepath.Join(tmpDir, assetName)
	got, err := download(ctx, fmt.Sprintf(downloadURL, version, assetName), archive, progress)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	if got != want {
		return fmt.Errorf("%s checksum mismatch: downloaded %s, release lists %s", assetName, got, want)
	}
	newBin := filepath.Join(tmpDir, binName)
	if runtime.GOOS == "windows" {
		err = extractZip(archive, binName, newBin)
	} else {
		err = extractTarGz(archive, binName, newBin)
	}
	if err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}
	return swap(exe, newBin)
}

// executable resolves the running binary's real path.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot determine binary path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("cannot resolve binary path: %w", err)
	}
	return exe, nil
}

// swap moves newBin into exe's place. On POSIX that is one atomic rename:
// the path always names either the old binary or the new one, the running
// process keeps its inode, and a failure leaves the old file untouched.
// Windows cannot replace a running executable, but it can rename it, so
// there the old binary is moved aside first and Cleanup removes it on the
// next launch.
func swap(exe, newBin string) error {
	backup := exe + backupSuffix
	if runtime.GOOS == "windows" {
		if err := os.Rename(exe, backup); err != nil {
			return fmt.Errorf("cannot move current binary aside: %w", err)
		}
	}
	if err := os.Rename(newBin, exe); err != nil {
		if runtime.GOOS == "windows" {
			_ = os.Rename(backup, exe)
		}
		return fmt.Errorf("cannot install update: %w", err)
	}
	return nil
}

// Cleanup removes what an earlier update left behind: the old binary Windows
// moved aside, and download directories abandoned by a session that exited
// mid-install. Called once at startup.
func Cleanup() {
	exe, err := executable()
	if err != nil {
		return
	}
	_ = os.Remove(exe + backupSuffix)
	dirs, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), tempPrefix+"*"))
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && time.Since(info.ModTime()) > staleAfter {
			_ = os.RemoveAll(dir)
		}
	}
}

// download writes url to dest and returns the SHA-256 of what it wrote, so
// the checksum is taken from the bytes on disk without a second pass.
func download(ctx context.Context, url, dest string, progress func(written, total int64)) (string, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var body io.Reader = resp.Body
	if progress != nil {
		body = &progressReader{r: resp.Body, total: resp.ContentLength, report: progress}
	}
	sum := sha256.New()
	if err := writeFile(dest, io.TeeReader(body, sum), 0o644); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// get issues a GET and returns the response only when it is a 200.
func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return resp, nil
}

// progressReader reports the running byte count after every chunk.
type progressReader struct {
	r      io.Reader
	total  int64
	read   int64
	report func(read, total int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.read += int64(n)
	pr.report(pr.read, pr.total)
	return n, err
}

// extractTarGz writes the archive entry called name to dest. A release
// archive holds exactly that one file, so nothing else is unpacked and no
// path inside the archive ever decides where bytes land.
func extractTarGz(archive, name, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not in archive", name)
		}
		if err != nil {
			return err
		}
		if header.Typeflag == tar.TypeReg && header.Name == name {
			return writeFile(dest, tr, os.FileMode(header.Mode))
		}
	}
}

// extractZip is extractTarGz for the Windows release archive.
func extractZip(archive, name, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeFile(dest, rc, f.Mode())
	}
	return fmt.Errorf("%s not in archive", name)
}

func writeFile(path string, src io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
