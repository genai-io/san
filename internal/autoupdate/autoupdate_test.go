package autoupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.22.9", "1.22.8", true},
		{"1.23.0", "1.22.99", true},
		{"2.0.0", "1.99.99", true},
		{"v1.22.9", "v1.22.8", true},
		{"1.22.8", "1.22.8", false},
		{"1.22.8", "1.22.9", false},                   // never downgrade
		{"1.22.9", "v1.22.8-3-gabc1234-dirty", false}, // dev build stays put
		{"1.22.9", "abc1234", false},
		{"1.22.9", "dev", false},
		{"1.22", "1.21.0", false},
		{"", "", false},
	}
	for _, tc := range tests {
		if got := Newer(tc.a, tc.b); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{"1.22.8": true, "v1.22.8": true, "v1.22.8-3-gabc1234-dirty": false, "abc1234": false, "dev": false} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", v, got, want)
		}
	}
}

func withLatestURL(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	old := latestURL
	latestURL = srv.URL
	t.Cleanup(func() { latestURL = old })
}

func TestLatest(t *testing.T) {
	withLatestURL(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Location", "https://github.com/genai-io/san/releases/tag/v1.21.0")
		w.WriteHeader(http.StatusFound)
	})
	got, err := Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}
	if got != "1.21.0" {
		t.Errorf("Latest() = %q, want %q", got, "1.21.0")
	}
}

func TestLatestRejectsBadResponses(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"500": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		"foreign redirect": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://example.com/something-else")
			w.WriteHeader(http.StatusFound)
		},
	} {
		t.Run(name, func(t *testing.T) {
			withLatestURL(t, handler)
			if _, err := Latest(context.Background()); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestDownloadReportsProgress(t *testing.T) {
	content := []byte("hello san binary content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "san.tar.gz")
	var written, total int64
	sum, err := download(context.Background(), srv.URL, dest, func(w, tot int64) { written, total = w, tot })
	if err != nil {
		t.Fatalf("download() error: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if !bytes.Equal(data, content) {
		t.Errorf("downloaded content = %q, want %q", data, content)
	}
	if written != int64(len(content)) || total != int64(len(content)) {
		t.Errorf("progress = (%d, %d), want (%d, %d)", written, total, len(content), len(content))
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(content)); sum != want {
		t.Errorf("sha256 = %s, want %s", sum, want)
	}
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "x"), nil); err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func tarGz(t *testing.T, name string, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gzw.Close()
	path := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractTarGz(t *testing.T) {
	content := []byte("#!/bin/bash\necho hello")
	dest := filepath.Join(t.TempDir(), "san")
	if err := extractTarGz(tarGz(t, "san", content), "san", dest); err != nil {
		t.Fatalf("extractTarGz() error: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("extracted content = %q, want %q", data, content)
	}
	if info, _ := os.Stat(dest); info.Mode()&0o111 == 0 {
		t.Errorf("extracted binary mode = %v, want executable", info.Mode())
	}
}

func TestExtractTarGzRejectsInvalidArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.tar.gz")
	os.WriteFile(path, []byte("not-a-tar-gz"), 0o644)
	if err := extractTarGz(path, "san", filepath.Join(t.TempDir(), "san")); err == nil {
		t.Fatal("expected error for invalid archive, got nil")
	}
}

func TestExtractTarGzRequiresTheNamedEntry(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "san")
	if err := extractTarGz(tarGz(t, "../escaped", []byte("evil")), "san", dest); err == nil {
		t.Fatal("expected error when the archive lacks the binary, got nil")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("nothing should be written when the entry is missing")
	}
}

func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	content := []byte("windows binary from zip")
	fw, err := zw.Create("san.exe")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	zw.Close()

	dir := t.TempDir()
	zipPath := filepath.Join(dir, "san_windows_amd64.zip")
	os.WriteFile(zipPath, buf.Bytes(), 0o644)

	dest := filepath.Join(dir, "san.exe")
	if err := extractZip(zipPath, "san.exe", dest); err != nil {
		t.Fatalf("extractZip() error: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if !bytes.Equal(data, content) {
		t.Errorf("extracted content = %q, want %q", data, content)
	}
	if err := extractZip(zipPath, "other.exe", dest); err == nil {
		t.Fatal("expected error when the archive lacks the binary, got nil")
	}
}

func TestExtractZipRejectsInvalidArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.zip")
	os.WriteFile(path, []byte("not-a-zip"), 0o644)
	if err := extractZip(path, "san.exe", filepath.Join(t.TempDir(), "san.exe")); err == nil {
		t.Fatal("expected error for invalid zip, got nil")
	}
}

func TestSwapReplacesInPlaceAndLeavesTheOldBinaryOnFailure(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "san")
	os.WriteFile(exe, []byte("old"), 0o755)
	newBin := filepath.Join(dir, "san-new")
	os.WriteFile(newBin, []byte("new"), 0o755)

	if err := swap(exe, newBin); err != nil {
		t.Fatalf("swap() error: %v", err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "new" {
		t.Errorf("exe = %q, want %q", data, "new")
	}
	if _, err := os.Stat(newBin); !os.IsNotExist(err) {
		t.Error("new binary should have been moved, not copied")
	}

	// A missing new binary leaves the current one exactly where it was.
	if err := swap(exe, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error for missing new binary")
	}
	if data, _ := os.ReadFile(exe); string(data) != "new" {
		t.Errorf("exe after failed swap = %q, want the current binary untouched", data)
	}
	if _, err := os.Stat(exe + backupSuffix); !os.IsNotExist(err) {
		t.Error("no backup should linger after a failed swap")
	}
}

func TestCleanup(t *testing.T) {
	exe, err := executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(exe)
	backup := exe + backupSuffix
	stale := filepath.Join(dir, tempPrefix+"stale")
	fresh := filepath.Join(dir, tempPrefix+"fresh")
	os.WriteFile(backup, []byte("stale backup"), 0o644)
	os.Mkdir(stale, 0o755)
	os.Mkdir(fresh, 0o755)
	t.Cleanup(func() { os.Remove(backup); os.RemoveAll(stale); os.RemoveAll(fresh) })
	old := time.Now().Add(-2 * staleAfter)
	os.Chtimes(stale, old, old)

	Cleanup()

	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("backup should have been removed")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale download dir should have been removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh download dir must be left alone: another session may be mid-download")
	}
}
