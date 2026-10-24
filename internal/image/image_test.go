package image

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/to/image.png")
	if err == nil {
		t.Error("Expected error for non-existent file, got nil")
	}
}

func TestLoad_UnsupportedFormat(t *testing.T) {
	// Create a temp file with unsupported extension
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(tmpFile)
	if err == nil {
		t.Error("Expected error for unsupported format, got nil")
	}
}

func TestLoadRejectsMismatchedImageExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mislabeled.png")
	if err := os.WriteFile(path, []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("JPEG bytes with a PNG extension must not be sent as image/png")
	}
}

func Test_supportedTypes(t *testing.T) {
	// Verify all expected types are supported
	expectedTypes := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".webp": "image/webp",
		".gif":  "image/gif",
	}

	for ext, mimeType := range expectedTypes {
		if supportedTypes[ext] != mimeType {
			t.Errorf("supportedTypes[%q] = %q, want %q", ext, supportedTypes[ext], mimeType)
		}
	}
}
