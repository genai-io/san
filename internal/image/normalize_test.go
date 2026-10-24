package image

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func attachmentBytes(t *testing.T, encoded string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxImageSize {
		t.Fatalf("payload %d exceeds %d", len(data), maxImageSize)
	}
	return data
}

func TestLoadRealLargePNG(t *testing.T) {
	path, err := filepath.Abs("../../assets/san.png")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= maxImageSize {
		t.Fatalf("expected real fixture to exceed payload cap, got %d", info.Size())
	}
	img, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if img.Path != path || img.FileName != "san"+extForMediaType(img.MediaType) {
		t.Fatalf("incorrect path/name: %q %q", img.Path, img.FileName)
	}
	data := attachmentBytes(t, img.Data)
	t.Logf("san.png: %d input bytes -> %d %s bytes", info.Size(), len(data), img.MediaType)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || "image/"+format != img.MediaType || cfg.Width > 2000 || cfg.Height > 2000 {
		t.Fatalf("invalid normalized image: %v %s %dx%d %s", err, format, cfg.Width, cfg.Height, img.MediaType)
	}
	original, err := os.ReadFile(path)
	if err != nil || len(original) != int(info.Size()) {
		t.Fatalf("source changed: %v", err)
	}
}

func TestClipboardNormalizeAndPersist(t *testing.T) {
	data, err := os.ReadFile("../../assets/san.png")
	if err != nil {
		t.Fatal(err)
	}
	img, err := newClipboardImage(data)
	if err != nil {
		t.Fatal(err)
	}
	if img.Path != "" || !strings.HasSuffix(img.FileName, extForMediaType(img.MediaType)) {
		t.Fatalf("incorrect clipboard metadata: %+v", img)
	}
	encoded := attachmentBytes(t, img.Data)
	dir := t.TempDir()
	path, err := EnsureFilePath(*img, dir)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, encoded) || !strings.HasSuffix(path, extForMediaType(img.MediaType)) {
		t.Fatalf("clipboard persistence mismatch: %v", err)
	}
}

func TestSmallFormatsKeepBytes(t *testing.T) {
	picture := image.NewRGBA(image.Rect(0, 0, 4, 3))
	picture.Set(1, 1, color.RGBA{255, 0, 0, 255})
	for _, tc := range []struct {
		ext, media string
		encode     func(io.Writer) error
	}{
		{".png", "image/png", func(w io.Writer) error { return png.Encode(w, picture) }},
		{".jpeg", "image/jpeg", func(w io.Writer) error { return jpeg.Encode(w, picture, nil) }},
		{".gif", "image/gif", func(w io.Writer) error { return gif.Encode(w, picture, nil) }},
	} {
		t.Run(tc.ext, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tc.encode(&buf); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "small"+tc.ext)
			if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			img, err := Load(path)
			if err != nil || img.MediaType != tc.media || !bytes.Equal(attachmentBytes(t, img.Data), buf.Bytes()) {
				t.Fatalf("Load: %v, media %q", err, img.MediaType)
			}
		})
	}
}

func TestWebP(t *testing.T) {
	// 1x1 WebP (the decoder is registered by normalize.go).
	data, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pixel.webp")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	img, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if img.MediaType != "image/webp" || !bytes.Equal(attachmentBytes(t, img.Data), data) {
		t.Fatalf("WebP metadata or contents changed: %s", img.MediaType)
	}
}

func TestLimitsAndInvalidData(t *testing.T) {
	if _, err := newClipboardImage(bytes.Repeat([]byte{0}, maxInputSize+1)); err == nil {
		t.Fatal("unbounded clipboard input")
	}
	if _, err := newClipboardImage([]byte("not a PNG")); err == nil {
		t.Fatal("invalid clipboard data")
	}
	if _, err := readImageBytes(bytes.NewReader(make([]byte, maxInputSize+1))); err == nil {
		t.Fatal("unbounded file read")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 4500, 4500))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := normalize(buf.Bytes(), "image/png"); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("expected decoded pixel limit, got %v", err)
	}
}

func TestIncompressiblePNGUsesJPEG(t *testing.T) {
	// Random opaque pixels force an oversized PNG even within 2000 pixels.
	pic := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	noise := rand.New(rand.NewSource(7))
	for i := 0; i < len(pic.Pix); i += 4 {
		noise.Read(pic.Pix[i : i+3])
		pic.Pix[i+3] = 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, pic); err != nil {
		t.Fatal(err)
	}
	if buf.Len() <= maxImageSize {
		t.Fatalf("test PNG too small: %d", buf.Len())
	}
	data, typ, err := normalize(buf.Bytes(), "image/png")
	if err != nil || typ != "image/jpeg" || len(data) > maxImageSize {
		t.Fatalf("fallback failed: %v %s %d", err, typ, len(data))
	}
}
