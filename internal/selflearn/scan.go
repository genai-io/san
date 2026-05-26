package selflearn

import (
	"fmt"
	"regexp"
	"strings"
)

// Memory content is injected verbatim into a future system prompt, so a
// poisoned entry is a stored prompt-injection / exfiltration vector. Content
// that trips a pattern is rejected at write time. This is a coarse guard, not a
// sandbox — it catches the obvious payloads.

var memoryThreatPatterns = []struct {
	re *regexp.Regexp
	id string
}{
	{regexp.MustCompile(`(?i)ignore\s+(previous|all|above|prior)\s+instructions`), "prompt_injection"},
	{regexp.MustCompile(`(?i)disregard\s+(your|all|any)\s+(instructions|rules|guidelines)`), "disregard_rules"},
	{regexp.MustCompile(`(?i)you\s+are\s+now\s+`), "role_hijack"},
	{regexp.MustCompile(`(?i)do\s+not\s+tell\s+the\s+user`), "deception_hide"},
	{regexp.MustCompile(`(?i)system\s+prompt\s+override`), "sys_prompt_override"},
	{regexp.MustCompile(`(?i)(curl|wget)\s+[^\n]*\$?\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`), "exfil"},
	{regexp.MustCompile(`(?i)(cat|less|more)\s+[^\n]*(\.env|credentials|\.netrc|\.pgpass|\.npmrc|\.pypirc)`), "read_secrets"},
	{regexp.MustCompile(`authorized_keys`), "ssh_backdoor"},
}

// invisibleRunes are zero-width / bidi-control code points that have no business
// in a durable memory entry and are a classic injection-hiding trick. Listed by
// code point so the source file stays free of literal invisible characters.
var invisibleRunes = map[rune]struct{}{
	0x200B: {}, // zero-width space
	0x200C: {}, // zero-width non-joiner
	0x200D: {}, // zero-width joiner
	0x2060: {}, // word joiner
	0xFEFF: {}, // zero-width no-break space / BOM
	0x202A: {}, // left-to-right embedding
	0x202B: {}, // right-to-left embedding
	0x202C: {}, // pop directional formatting
	0x202D: {}, // left-to-right override
	0x202E: {}, // right-to-left override
}

func scanMemoryContent(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("content cannot be empty")
	}
	for _, r := range content {
		if _, bad := invisibleRunes[r]; bad {
			return fmt.Errorf("rejected: content contains an invisible unicode character (U+%04X)", r)
		}
	}
	for _, p := range memoryThreatPatterns {
		if p.re.MatchString(content) {
			return fmt.Errorf("rejected: content matches threat pattern %q; memory is injected into the system prompt and must not carry injection/exfiltration payloads", p.id)
		}
	}
	return nil
}
