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
	frontmatter, body, err = splitFrontmatter(src, func(line string, _ bool) bool {
		return strings.TrimSpace(line) == "---"
	})
	return frontmatter, strings.TrimSpace(body), err
}

// SplitFrontmatter separates a leading YAML fence from the body without
// rewriting either. Both delimiters must be complete LF or CRLF lines.
func SplitFrontmatter(src string) (frontmatter, body string, err error) {
	return splitFrontmatter(src, func(line string, newline bool) bool {
		return newline && strings.TrimSuffix(line, "\r") == "---"
	})
}

func splitFrontmatter(src string, delimiter func(string, bool) bool) (frontmatter, body string, err error) {
	first, rest, newline := strings.Cut(src, "\n")
	if !delimiter(first, newline) {
		return "", src, nil
	}
	for remaining := rest; remaining != ""; {
		line, next, newline := strings.Cut(remaining, "\n")
		if delimiter(line, newline) {
			return rest[:len(rest)-len(remaining)], next, nil
		}
		remaining = next
	}
	return "", "", fmt.Errorf("unclosed frontmatter: missing closing '---' delimiter")
}
