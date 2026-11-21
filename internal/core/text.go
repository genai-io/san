package core

import "unicode/utf8"

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
