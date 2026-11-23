package core

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// UTF8Prefix returns at most limit bytes without splitting a UTF-8 rune.
// It does not normalize invalid input.
func UTF8Prefix(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// ValidLabel checks a required single-line model-proposed field with a byte limit.
func ValidLabel(s string, maxBytes int) bool {
	return s != "" && len(s) <= maxBytes && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) < 0
}

// ValidMultiline checks a model-proposed field, allowing line breaks and tabs.
func ValidMultiline(s string, maxBytes int) bool {
	return len(s) <= maxBytes && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r'
	}) < 0
}
