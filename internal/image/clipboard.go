package image

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/genai-io/san/v2/internal/core"
)

// readImageFromClipboard reads an image from the clipboard.
// Returns nil, nil if no image is available (not an error).
func readImageFromClipboard() (*ImageInfo, error) {
	switch runtime.GOOS {
	case "darwin":
		return readClipboardMacOS()
	case "linux":
		return readClipboardLinux()
	default:
		return nil, fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
}

// newClipboardImageInfo creates an ImageInfo from clipboard PNG data.
// Returns nil, nil if data is empty.
func newClipboardImageInfo(data []byte) (*ImageInfo, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if len(data) > maxImageSize {
		return nil, fmt.Errorf("clipboard image too large: %d bytes (max %d)", len(data), maxImageSize)
	}
	return &ImageInfo{
		MediaType: "image/png",
		Data:      data,
		Size:      len(data),
		FileName:  fmt.Sprintf("clipboard_%s.png", time.Now().Format("150405")),
	}, nil
}

// readClipboardMacOS reads image from macOS clipboard using osascript.
func readClipboardMacOS() (*ImageInfo, error) {
	tmp, err := os.CreateTemp("", "clipboard_*.png")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpFile := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpFile) }()

	script := fmt.Sprintf(`
		set theFile to POSIX file "%s"
		try
			set imgData to the clipboard as «class PNGf»
			set fileRef to open for access theFile with write permission
			write imgData to fileRef
			close access fileRef
			return "ok"
		on error
			return "no image"
		end try
	`, tmpFile)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to read clipboard: %w", err)
	}

	if strings.TrimSpace(string(output)) == "no image" {
		return nil, nil
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read clipboard image: %w", err)
	}

	return newClipboardImageInfo(data)
}

// readClipboardLinux reads image from Linux clipboard using xclip or xsel.
func readClipboardLinux() (*ImageInfo, error) {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	data, err := cmd.Output()
	if err != nil {
		cmd = exec.Command("xsel", "--clipboard", "--output")
		data, err = cmd.Output()
		if err != nil {
			return nil, nil
		}
	}
	return newClipboardImageInfo(data)
}

// ReadImageToProviderData reads clipboard image directly to core.Image
func ReadImageToProviderData() (*core.Image, error) {
	info, err := readImageFromClipboard()
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, nil
	}

	data := info.ToProviderData()
	return &data, nil
}
