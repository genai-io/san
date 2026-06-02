package selflearn

import (
	"fmt"
	"regexp"
	"strings"
)

// Memory entries and skill bodies are injected verbatim into a future system
// prompt, so a poisoned one is a stored prompt-injection / exfiltration vector.
// Content that trips a pattern is rejected at write time. This is a coarse
// guard, not a sandbox — it catches the obvious payloads.

var threatPatterns = []struct {
	re *regexp.Regexp
	id string
}{
	{regexp.MustCompile(`(?i)ignore\s+(previous|all|above|prior)\s+instructions`), "prompt_injection"},
	{regexp.MustCompile(`(?i)disregard\s+(your|all|any)\s+(instructions|rules|guidelines)`), "disregard_rules"},
	// role_hijack: only fire on classic role-assignment jailbreaks like
	// "you are now an admin / root / unrestricted / in developer mode".
	// The unanchored `(?i)you are now ` matched benign English (e.g.
	// "you are now in the repo root, run make ci"), rejecting legitimate
	// memory entries. The role keyword anchor keeps the spirit (catch
	// classic prompt-injection role swaps) without the false positives.
	{regexp.MustCompile(`(?i)you\s+are\s+now\s+(an?\s+|the\s+)?(admin|administrator|root|superuser|developer|operator|assistant|jailbroken|unrestricted|free\s+from|in\s+(debug|developer|admin|god|safe)\s+mode)`), "role_hijack"},
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

// scanContent rejects empty input and then applies the threat scan. Use it for
// required, non-empty content (memory entries, skill bodies).
func scanContent(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("content cannot be empty")
	}
	return scanForThreats(content)
}

// scanForThreats applies the injection/exfiltration guard without requiring the
// content to be non-empty. Use it for optional or deletion-capable fields
// (skill descriptions, patch replacements, support files) where empty is valid.
func scanForThreats(content string) error {
	for _, r := range content {
		if _, bad := invisibleRunes[r]; bad {
			return fmt.Errorf("rejected: content contains an invisible unicode character (U+%04X)", r)
		}
	}
	for _, p := range threatPatterns {
		if p.re.MatchString(content) {
			return fmt.Errorf("rejected: content matches threat pattern %q; this text is injected into the system prompt and must not carry injection/exfiltration payloads", p.id)
		}
	}
	return nil
}

// scanNewThreats fails only when candidate trips a threat pattern that
// original did not. Used by Patch so a skill body that legitimately quotes
// a threat-pattern substring (e.g. a defense-against-injection example)
// remains patchable, while still blocking patches that introduce a NEW
// payload. Invisible runes are still always rejected — they have no
// legitimate use in a skill body and a patch must not be allowed to
// smuggle them in regardless of what the original contained.
func scanNewThreats(original, candidate string) error {
	for _, r := range candidate {
		if _, bad := invisibleRunes[r]; bad {
			return fmt.Errorf("rejected: content contains an invisible unicode character (U+%04X)", r)
		}
	}
	origMatches := matchedThreatIDs(original)
	for _, p := range threatPatterns {
		if !p.re.MatchString(candidate) {
			continue
		}
		if origMatches[p.id] {
			continue // already in original — patch isn't introducing it
		}
		return fmt.Errorf("rejected: patch introduces new threat pattern %q; this text is injected into the system prompt and must not carry injection/exfiltration payloads", p.id)
	}
	return nil
}

// matchedThreatIDs returns the set of threat-pattern IDs that match content.
func matchedThreatIDs(content string) map[string]bool {
	out := make(map[string]bool, len(threatPatterns))
	for _, p := range threatPatterns {
		if p.re.MatchString(content) {
			out[p.id] = true
		}
	}
	return out
}
