// Package image provides image loading, validation, and encoding utilities.
package image

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/v2/internal/core"
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

// ImageInfo holds information about a loaded image
type ImageInfo struct {
	Path      string
	MediaType string
	Data      []byte
	Size      int
	FileName  string
}

// Load loads and validates an image from the given path
func Load(path string) (*ImageInfo, error) {
	// Resolve path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	// Check if file exists
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", path)
		}
		return nil, fmt.Errorf("cannot access file: %w", err)
	}

	// Check file size
	if info.Size() > maxImageSize {
		return nil, fmt.Errorf("image too large: %d bytes (max %d)", info.Size(), maxImageSize)
	}

	// Check extension
	ext := strings.ToLower(filepath.Ext(absPath))
	mediaType, ok := supportedTypes[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported image format: %s", ext)
	}

	// Read file
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Detect actual content type to verify
	detectedType := http.DetectContentType(data)
	if !strings.HasPrefix(detectedType, "image/") {
		return nil, fmt.Errorf("file is not a valid image")
	}

	return &ImageInfo{
		Path:      absPath,
		MediaType: mediaType,
		Data:      data,
		Size:      len(data),
		FileName:  filepath.Base(absPath),
	}, nil
}

// ToBase64 returns the image data as a base64 encoded string
func (i *ImageInfo) ToBase64() string {
	return base64.StdEncoding.EncodeToString(i.Data)
}

// ToProviderData converts ImageInfo to core.Image
func (i *ImageInfo) ToProviderData() core.Image {
	return core.Image{
		MediaType: i.MediaType,
		Data:      i.ToBase64(),
		FileName:  i.FileName,
		Size:      i.Size,
	}
}
