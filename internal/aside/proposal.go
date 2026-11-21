package aside

import (
	"strings"
	"unicode"
)

// ValidLabel checks a required single-line proposal field with a byte limit.
func ValidLabel(s string, maxBytes int) bool {
	return s != "" && len(s) <= maxBytes && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) < 0
}

// ValidMultiline checks a proposal field, allowing line breaks and tabs.
func ValidMultiline(s string, maxBytes int) bool {
	return len(s) <= maxBytes && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r'
	}) < 0
}
