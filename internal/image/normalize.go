package image

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"path/filepath"
	"strings"

	_ "image/gif"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxInputSize = 32 * 1024 * 1024 // Bound untrusted compressed input before decoding.
	maxImageSize = 5 * 1024 * 1024  // Bound binary bytes before base64 encoding for providers.
	maxPixels    = 20 * 1000 * 1000 // At most ~80 MB for an RGBA decode.
	maxDimension = 2000
)

// readImageBytes caps both file and clipboard temporary-file reads, including
// files that grow between stat and read.
func readImageBytes(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxInputSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInputSize {
		return nil, fmt.Errorf("image input too large (max %d bytes)", maxInputSize)
	}
	return data, nil
}

// normalize validates even images small enough to send untouched. Large images
// are resized and/or re-encoded; never label the output with the input's type.
func normalize(data []byte, expectedType string) ([]byte, string, error) {
	if len(data) > maxInputSize {
		return nil, "", fmt.Errorf("image input too large (max %d bytes)", maxInputSize)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("invalid image: %w", err)
	}
	mediaType := "image/" + format
	if mediaType != expectedType {
		return nil, "", fmt.Errorf("image content type %s does not match %s", mediaType, expectedType)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, "", fmt.Errorf("image dimensions %dx%d exceed %d pixels", cfg.Width, cfg.Height, maxPixels)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("invalid image: %w", err)
	}
	if len(data) <= maxImageSize && cfg.Width <= maxDimension && cfg.Height <= maxDimension {
		return data, mediaType, nil
	}
	w, h := cfg.Width, cfg.Height
	if w > maxDimension || h > maxDimension {
		scale := math.Min(float64(maxDimension)/float64(w), float64(maxDimension)/float64(h))
		w = max(1, int(math.Round(float64(w)*scale)))
		h = max(1, int(math.Round(float64(h)*scale)))
	}
	var resized image.Image = img
	if w != cfg.Width || h != cfg.Height {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
		resized = dst
	}
	// Keep lossless pixels and transparency when they fit. JPEG input already
	// has no alpha and does not benefit from a PNG round trip.
	if format != "jpeg" {
		var buf bytes.Buffer
		if err := png.Encode(&limitedWriter{w: &buf, remaining: maxImageSize}, resized); err == nil {
			return buf.Bytes(), "image/png", nil
		}
	}
	// JPEG has no alpha: composite onto white rather than silently turning
	// transparent pixels black. This is a last resort for large PNGs/GIFs.
	opaque := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(opaque, opaque.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(opaque, opaque.Bounds(), resized, resized.Bounds().Min, draw.Over)
	for _, quality := range []int{85, 75, 65} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&limitedWriter{w: &buf, remaining: maxImageSize}, opaque, &jpeg.Options{Quality: quality}); err == nil {
			return buf.Bytes(), "image/jpeg", nil
		}
	}
	return nil, "", fmt.Errorf("image cannot be encoded within %d bytes", maxImageSize)
}

// limitedWriter returns an error instead of accumulating an unbounded encode
// or clipboard subprocess output.
type limitedWriter struct {
	w         io.Writer
	remaining int
}

var errImageLimit = errors.New("image exceeds size limit")

func (l *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > l.remaining {
		return 0, errImageLimit
	}
	n, err := l.w.Write(p)
	l.remaining -= n
	return n, err
}

// normalizedName reflects the actual encoded content, without changing the
// original path used by disk-backed attachments.
func normalizedName(name, mediaType string) string {
	return strings.TrimSuffix(name, filepath.Ext(name)) + extForMediaType(mediaType)
}
