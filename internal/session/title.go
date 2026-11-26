package session

import (
	"strings"
	"unicode/utf8"

	"github.com/genai-io/san/internal/core"
)

const (
	maxTitleLen          = 60
	MinSubstantiveLength = 6
)

func GenerateTitle(msgs []core.Message) string {
	var fallback string
	for _, msg := range msgs {
		text, ok := extractUserText(msg)
		if !ok {
			continue
		}
		if fallback == "" {
			fallback = text
		}
		if utf8.RuneCountInString(text) >= MinSubstantiveLength {
			return truncateTitle(text)
		}
	}
	if fallback != "" {
		return truncateTitle(fallback)
	}
	return "Untitled Session"
}

func truncateTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxTitleLen {
		return s
	}
	runes := []rune(s)[:maxTitleLen]
	for i := maxTitleLen - 1; i > maxTitleLen/2; i-- {
		if runes[i] == ' ' {
			runes = runes[:i]
			break
		}
	}
	return strings.TrimSpace(string(runes)) + "..."
}
