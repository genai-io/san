// Package image provides image loading, validation, and encoding utilities.
package image

import (
	"github.com/genai-io/sdk-go/pkg/ai"

	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/core"
)

const (
	// maxImageSize is the maximum allowed image size (5MB)
	maxImageSize = 5 * 1024 * 1024
)

// supportedTypes maps file extensions to MIME types
var supportedTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}

// Load reads, validates, and base64-encodes an image from the given path.
func Load(path string) (core.Attachment, error) {
	// Resolve path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return core.Attachment{}, fmt.Errorf("invalid path: %w", err)
	}

	// Check if file exists
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return core.Attachment{}, fmt.Errorf("file not found: %s", path)
		}
		return core.Attachment{}, fmt.Errorf("cannot access file: %w", err)
	}

	// Check file size
	if info.Size() > maxImageSize {
		return core.Attachment{}, fmt.Errorf("image too large: %d bytes (max %d)", info.Size(), maxImageSize)
	}

	// Check extension
	ext := strings.ToLower(filepath.Ext(absPath))
	mediaType, ok := supportedTypes[ext]
	if !ok {
		return core.Attachment{}, fmt.Errorf("unsupported image format: %s", ext)
	}

	// Read file
	data, err := os.ReadFile(absPath)
	if err != nil {
		return core.Attachment{}, fmt.Errorf("failed to read file: %w", err)
	}

	// Detect actual content type to verify
	detectedType := http.DetectContentType(data)
	if !strings.HasPrefix(detectedType, "image/") {
		return core.Attachment{}, fmt.Errorf("file is not a valid image")
	}

	return newImage(mediaType, filepath.Base(absPath), absPath, data), nil
}

// newImage builds a core.Attachment from raw bytes, base64-encoding the data.
func newImage(mediaType, fileName, path string, data []byte) core.Attachment {
	return core.Attachment{
		Image: ai.Image{
			MediaType: mediaType,
			Data:      base64.StdEncoding.EncodeToString(data),
			FileName:  fileName,
		},
		Path: path,
	}
}

// EnsureFilePath returns a filesystem path for an image, so a tool handed the
// path (an MCP image describer, say) can always open it. An image loaded from
// disk keeps its own path. A clipboard paste has no backing file, so its bytes
// are written under dir, named by their hash: pasting the same picture twice
// lands on one file, and the name says nothing about when it was pasted.
func EnsureFilePath(img core.Attachment, dir string) (string, error) {
	if img.Path != "" {
		return img.Path, nil
	}
	if img.Data == "" {
		return "", fmt.Errorf("image %s has neither a path nor data", img.FileName)
	}
	data, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		return "", fmt.Errorf("decoding image data: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	path := filepath.Join(dir, hex.EncodeToString(sum[:8])+extForMediaType(img.MediaType))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// extForMediaType picks the file extension a pasted image should carry. It does
// not reverse supportedTypes: two extensions map to image/jpeg there, and Go
// randomises map iteration, so the same image would land on .jpg or .jpeg from
// run to run.
func extForMediaType(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}
