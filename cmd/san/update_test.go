package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		resp := releaseInfo{
			TagName: "v1.21.0",
			Assets: []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			}{
				{Name: "san_darwin_amd64.tar.gz", BrowserDownloadURL: "https://example.com/san_darwin_amd64.tar.gz"},
				{Name: "san_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/san_linux_amd64.tar.gz"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	oldURL := githubAPI
	githubAPI = srv.URL
	defer func() { githubAPI = oldURL }()

	release, err := fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestRelease() error: %v", err)
	}
	if release.TagName != "v1.21.0" {
		t.Errorf("TagName = %q, want %q", release.TagName, "v1.21.0")
	}
	if len(release.Assets) != 2 {
		t.Errorf("len(Assets) = %d, want 2", len(release.Assets))
	}
}

func TestFetchLatestRelease_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	oldURL := githubAPI
	githubAPI = srv.URL
	defer func() { githubAPI = oldURL }()

	_, err := fetchLatestRelease(context.Background())
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestFetchLatestRelease_EmptyTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := releaseInfo{TagName: ""}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	oldURL := githubAPI
	githubAPI = srv.URL
	defer func() { githubAPI = oldURL }()

	_, err := fetchLatestRelease(context.Background())
	if err == nil {
		t.Fatal("expected error for empty tag, got nil")
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
