package setting

import "fmt"

// MemoryPreviewLimits controls the initial MEMORY.md preview in both Asides.
type MemoryPreviewLimits struct {
	Lines int
	KB    int // 1 KB = 1,000 bytes
}

const (
	maxMemoryPreviewLines = 2000
	maxMemoryPreviewKB    = 128
)

func (v MemoryPreviewLimits) Resolve() MemoryPreviewLimits {
	if v.Lines < 1 || v.Lines > maxMemoryPreviewLines {
		v.Lines = 200
	}
	if v.KB < 1 || v.KB > maxMemoryPreviewKB {
		v.KB = 25
	}
	return v
}

func (d *Data) MemoryPreview() MemoryPreviewLimits {
	if d == nil {
		return (MemoryPreviewLimits{}).Resolve()
	}
	return (MemoryPreviewLimits{Lines: d.MemoryPreviewLines, KB: d.MemoryPreviewKB}).Resolve()
}

func (s *Settings) MemoryPreview() MemoryPreviewLimits {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.MemoryPreview()
}

// SaveMemoryPreviewLines saves a line limit. Zero clears the scoped override.
func SaveMemoryPreviewLines(cwd string, scope Scope, value int) error {
	if value < 0 || value > maxMemoryPreviewLines {
		return fmt.Errorf("enter 1–%d lines or leave blank to inherit", maxMemoryPreviewLines)
	}
	return updateMemorySettings(cwd, scope, func(d *Data) { d.MemoryPreviewLines = value })
}

// SaveMemoryPreviewKB saves a size limit. Zero clears the scoped override.
func SaveMemoryPreviewKB(cwd string, scope Scope, value int) error {
	if value < 0 || value > maxMemoryPreviewKB {
		return fmt.Errorf("enter 1–%d KB or leave blank to inherit", maxMemoryPreviewKB)
	}
	return updateMemorySettings(cwd, scope, func(d *Data) { d.MemoryPreviewKB = value })
}
