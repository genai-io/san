package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGoArch(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"amd64", "amd64"},
		{"arm64", "arm64"},
		{"x86_64", "x86_64"},
		{"", ""},
	}
	for _, tc := range tests {
		got := goArch(tc.input)
		if got != tc.want {
			t.Errorf("goArch(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFetchLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Location", "https://github.com/genai-io/san/releases/tag/v1.21.0")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	oldURL := githubLatestRelease
	githubLatestRelease = srv.URL
	defer func() { githubLatestRelease = oldURL }()

	release, err := fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestRelease() error: %v", err)
	}
	if release.TagName != "v1.21.0" {
		t.Errorf("TagName = %q, want %q", release.TagName, "v1.21.0")
	}
}

func TestFetchLatestRelease_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	oldURL := githubLatestRelease
	githubLatestRelease = srv.URL
	defer func() { githubLatestRelease = oldURL }()

	_, err := fetchLatestRelease(context.Background())
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestFetchLatestRelease_InvalidRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://example.com/something-else")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	oldURL := githubLatestRelease
	githubLatestRelease = srv.URL
	defer func() { githubLatestRelease = oldURL }()

	_, err := fetchLatestRelease(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid Location header, got nil")
	}
}

func TestDownloadWithProgress(t *testing.T) {
	content := []byte("hello san binary content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "san.tar.gz")

	err := downloadWithProgress(context.Background(), srv.URL, dest)
	if err != nil {
		t.Fatalf("downloadWithProgress() error: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("downloaded content = %q, want %q", string(data), string(content))
	}
}

func TestDownloadWithProgress_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "san.tar.gz")

	err := downloadWithProgress(context.Background(), srv.URL, dest)
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()

	// Build a tar.gz in memory containing a single "san" file
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	content := []byte("#!/bin/bash\necho hello")
	hdr := &tar.Header{
		Name: "san",
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gzw.Close()

	tarball := filepath.Join(dir, "bundle.tar.gz")
	if err := os.WriteFile(tarball, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(dir, "extracted")
	os.MkdirAll(destDir, 0755)

	if err := extractTarGz(tarball, destDir); err != nil {
		t.Fatalf("extractTarGz() error: %v", err)
	}

	extracted := filepath.Join(destDir, "san")
	data, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("extracted content = %q, want %q", string(data), string(content))
	}
}

func TestExtractTarGz_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "invalid.tar.gz")
	os.WriteFile(tarball, []byte("not-a-tar-gz"), 0644)

	err := extractTarGz(tarball, dir)
	if err == nil {
		t.Fatal("expected error for invalid archive, got nil")
	}
}

func TestExtractTarGz_PathTraversal(t *testing.T) {
	dir := t.TempDir()

	// Build a tar.gz whose entry escapes destDir via "../"
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	content := []byte("evil")
	hdr := &tar.Header{
		Name: "../escaped",
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gzw.Close()

	tarball := filepath.Join(dir, "evil.tar.gz")
	if err := os.WriteFile(tarball, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(dir, "extracted")
	os.MkdirAll(destDir, 0755)

	if err := extractTarGz(tarball, destDir); err == nil {
		t.Fatal("expected error for path traversal entry, got nil")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped")); err == nil {
		t.Fatal("path traversal escaped destDir")
	}
}

func TestExtractTarGz_WindowsBinary(t *testing.T) {
	dir := t.TempDir()

	// Build a tar.gz containing a "san.exe" entry (as expected on Windows)
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	content := []byte("windows binary content")
	hdr := &tar.Header{
		Name: "san.exe",
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gzw.Close()

	tarball := filepath.Join(dir, "bundle.tar.gz")
	if err := os.WriteFile(tarball, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(dir, "extracted")
	os.MkdirAll(destDir, 0755)

	if err := extractTarGz(tarball, destDir); err != nil {
		t.Fatalf("extractTarGz() error: %v", err)
	}

	extracted := filepath.Join(destDir, "san.exe")
	data, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("extracted content = %q, want %q", string(data), string(content))
	}
}

func TestExtractZip(t *testing.T) {
	dir := t.TempDir()

	// Build a zip in memory containing a single "san.exe" file
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	content := []byte("windows binary from zip")
	fw, err := zw.Create("san.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "san_windows_amd64.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	if err := extractZip(zipPath, dir); err != nil {
		t.Fatalf("extractZip() error: %v", err)
	}

	extracted := filepath.Join(dir, "san.exe")
	data, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("extracted content = %q, want %q", string(data), string(content))
	}
}

func TestExtractZip_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "invalid.zip")
	os.WriteFile(zipPath, []byte("not-a-zip"), 0644)

	err := extractZip(zipPath, dir)
	if err == nil {
		t.Fatal("expected error for invalid zip, got nil")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.bin")
	dst := filepath.Join(dir, "dest.bin")

	content := []byte("hello copy fallback")
	if err := os.WriteFile(src, content, 0755); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(dst, src); err != nil {
		t.Fatalf("copyFile() error: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("copied content = %q, want %q", string(data), string(content))
	}

	// Verify permissions were preserved
	srcInfo, _ := os.Stat(src)
	dstInfo, _ := os.Stat(dst)
	if srcInfo.Mode() != dstInfo.Mode() {
		t.Errorf("mode = %v, want %v", dstInfo.Mode(), srcInfo.Mode())
	}
}

func TestCopyFile_SrcNotFound(t *testing.T) {
	dir := t.TempDir()
	err := copyFile(filepath.Join(dir, "dest"), filepath.Join(dir, "nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent source, got nil")
	}
}

func TestCleanupUpdateBackup(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	backupPath := exe + ".bak"

	// Clean up any pre-existing backup file first
	os.Remove(backupPath)
	defer os.Remove(backupPath)

	// Create a dummy backup file to simulate a stale .bak from a previous update
	if err := os.WriteFile(backupPath, []byte("stale backup content"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Fatal("backup file should exist before cleanup")
	}

	// Call the startup cleanup function
	cleanupUpdateBackup()

	// Verify the backup was removed
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Error("backup file should have been removed by cleanupUpdateBackup")
	}
}

func TestCleanupUpdateBackup_NoFile(t *testing.T) {
	// Ensure no leftover backup from a previous test run
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	backupPath := exe + ".bak"
	os.Remove(backupPath)

	// Should not panic or error when no backup file exists
	cleanupUpdateBackup()
}

func TestCleanupUpdateBackup_ExecError(t *testing.T) {
	// Smoke test: runs cleanupUpdateBackup and verifies it doesn't panic
	cleanupUpdateBackup()
}

func TestProgressWriter(t *testing.T) {
	var buf bytes.Buffer
	pw := &progressWriter{
		w:     &buf,
		total: 100,
		done:  make(chan struct{}),
	}

	n, err := pw.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("wrote %d bytes, want 5", n)
	}
	if pw.written != 5 {
		t.Errorf("written = %d, want 5", pw.written)
	}
	if buf.String() != "hello" {
		t.Errorf("buf = %q, want %q", buf.String(), "hello")
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"whatever\n", false},
		{"", false},
	}
	for _, tc := range tests {
		// Save and restore stdin
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(tc.input))
		w.Close()
		os.Stdin = r

		got := confirm("test?")
		if got != tc.want {
			t.Errorf("confirm(%q) = %v, want %v", tc.input, got, tc.want)
		}
		os.Stdin = oldStdin
	}
}
