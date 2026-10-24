package input

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/genai-io/san/internal/core"
)

// ImageDisplay puts image markers where references appeared in the input and
// returns the attachments in that same order. ProcessImageRefs still owns
// loading/error policy; this only projects successfully loaded images for the
// conversation and the model's interleaved content.
func ImageDisplay(cwd, raw string, inline, files []core.Attachment) (string, []core.Attachment) {
	type imageAt struct {
		start, end int
		image      core.Attachment
		keepPath   bool // bare non-leading paths remain in Content
	}
	var refs []imageAt
	usedInline := make([]bool, len(inline))
	used := make([]bool, len(files))
	fileAt := func(path string) (core.Attachment, bool) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		for i, img := range files {
			if !used[i] && filepath.Clean(img.Path) == filepath.Clean(path) {
				used[i] = true
				return img, true
			}
		}
		return core.Attachment{}, false
	}

	for i, match := range core.InlineImageTokenRe.FindAllStringIndex(raw, -1) {
		if i < len(inline) {
			usedInline[i] = true
			refs = append(refs, imageAt{start: match[0], end: match[1], image: inline[i]})
		}
	}
	for _, match := range imageRefPattern.FindAllStringSubmatchIndex(raw, -1) {
		if img, ok := fileAt(raw[match[2]:match[3]]); ok {
			refs = append(refs, imageAt{start: match[0], end: match[1], image: img})
		}
	}
	leading := LeadingImagePath(cwd, raw)
	for _, match := range bareImagePathRe.FindAllStringSubmatchIndex(raw, -1) {
		// The bare-path regex also sees the path inside an @-reference.
		overlap := false
		for _, ref := range refs {
			if match[0] < ref.end && ref.start < match[1] {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		path := raw[match[2]:match[3]]
		if img, ok := fileAt(path); ok {
			refs = append(refs, imageAt{
				start: match[0], end: match[1], image: img,
				keepPath: path != leading || strings.TrimSpace(raw[:match[0]]) != "",
			})
		}
	}
	slices.SortFunc(refs, func(a, b imageAt) int { return a.start - b.start })
	var display strings.Builder
	ordered := make([]core.Attachment, 0, len(inline)+len(files))
	last := 0
	for _, ref := range refs {
		if ref.start < last {
			continue
		}
		display.WriteString(raw[last:ref.start])
		if ref.keepPath {
			display.WriteString(raw[ref.start:ref.end])
		}
		ordered = append(ordered, ref.image)
		fmt.Fprintf(&display, "[Image #%d]", len(ordered))
		last = ref.end
	}
	display.WriteString(raw[last:])
	// An attachment without a textual reference is still sent, even if it
	// cannot be positioned inline (e.g. an older queued item).
	for i, img := range inline {
		if !usedInline[i] {
			ordered = append(ordered, img)
		}
	}
	for i, img := range files {
		if !used[i] {
			ordered = append(ordered, img)
		}
	}
	return strings.TrimSpace(display.String()), ordered
}
