// Package markdown provides shared utilities for parsing markdown files.
package markdown

import (
	"fmt"
	"os"
	"strings"
)

// ParseFrontmatterFile reads a markdown file and returns (frontmatter, body).
// Frontmatter is the YAML content between opening and closing --- delimiters.
func ParseFrontmatterFile(path string) (frontmatter, body string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	src := strings.ReplaceAll(string(data), "\r\n", "\n")
	first, rest, _ := strings.Cut(src, "\n")
	if strings.TrimSpace(first) != "---" {
		return "", strings.TrimSpace(src), nil
	}
	var fm strings.Builder
	for rest != "" {
		line, next, _ := strings.Cut(rest, "\n")
		if strings.TrimSpace(line) == "---" {
			return fm.String(), strings.TrimSpace(next), nil
		}
		fm.WriteString(line + "\n")
		rest = next
	}
	return "", "", fmt.Errorf("unclosed frontmatter: missing closing '---' delimiter")
}
