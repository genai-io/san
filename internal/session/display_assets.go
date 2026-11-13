package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/genai-io/san/internal/atomicfile"
)

// copyDisplayAssets copies named display files, separately from immutable
// digest-addressed request blobs. Named assets are not blob-GC candidates.
func copyDisplayAssets(source, target string) error {
	for _, name := range []string{"tool-result", "images"} {
		src := filepath.Join(source, name)
		err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			dst := filepath.Join(target, name, rel)
			if entry.IsDir() {
				return os.MkdirAll(dst, 0o700)
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("display asset is not a regular file: %s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return atomicfile.Write(dst, data, 0o600)
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
