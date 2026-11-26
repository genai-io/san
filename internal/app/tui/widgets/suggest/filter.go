package suggest

import (
	"regexp"
	"strings"
)

var (
	aiVoicePrefixes = []string{
		"i'll", "i will", "let me", "here's", "here is", "here are",
		"i can", "i would", "i think", "i notice", "i'm",
		"that's", "this is", "this will",
		"you can", "you should", "you could",
		"sure,", "of course", "certainly",
	}
	evaluativePhrases = []string{
		"thanks", "thank you", "looks good", "sounds good",
		"that works", "that worked", "that's all",
		"nice", "great", "perfect", "makes sense",
		"awesome", "excellent", "good job",
	}
	prefixedLabelRe     = regexp.MustCompile(`^\w+:\s`)
	multipleSentencesRe = regexp.MustCompile(`[.!?]\s+[A-Z]`)
)

func FilterSuggestion(text string) string {
	text = strings.TrimSpace(text)
	if len(text) >= 2 && (text[0] == '"' && text[len(text)-1] == '"' ||
		text[0] == '\'' && text[len(text)-1] == '\'') {
		text = text[1 : len(text)-1]
		text = strings.TrimSpace(text)
	}

	if text == "" {
		return ""
	}
	if len(text) > 100 {
		return ""
	}
	words := strings.Fields(text)
	if len(words) > 12 {
		return ""
	}
	if len(words) < 2 && !isAllowedSingleWord(text) {
		return ""
	}
	if strings.ContainsAny(text, "*\n") {
		return ""
	}
	if multipleSentencesRe.MatchString(text) {
		return ""
	}
	if prefixedLabelRe.MatchString(text) {
		return ""
	}

	lower := strings.ToLower(text)

	for _, prefix := range aiVoicePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	for _, phrase := range evaluativePhrases {
		if strings.Contains(lower, phrase) {
			return ""
		}
	}

	return text
}

var allowedSingleWords = map[string]bool{
	"yes": true, "yeah": true, "yep": true, "yup": true,
	"no": true, "sure": true, "ok": true, "okay": true,
	"push": true, "commit": true, "deploy": true,
	"stop": true, "continue": true, "check": true,
	"exit": true, "quit": true,
}

func isAllowedSingleWord(word string) bool {
	if strings.HasPrefix(word, "/") {
		return true
	}
	return allowedSingleWords[strings.ToLower(word)]
}
