package automemory

import (
	"regexp"
	"strings"
)

const maxEvidence = 4096

// Request is an explicit maintenance instruction, constructed by ParseRequest.
// Its zero value does not authorize a write.
type Request struct {
	op, scope, target, evidence string
}

var (
	rememberRequest = regexp.MustCompile(`(?i)^remember(?: (user|project) memory)?:`)
	updateRequest   = regexp.MustCompile(`(?i)^update (user|project) memory ([a-z0-9_-]+\.md):`)
	forgetRequest   = regexp.MustCompile(`(?i)^forget (user|project) memory ([a-z0-9_-]+\.md)$`)
	noteName        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}\.md$`)
)

// ParseRequest accepts “remember: <fact>” (project), “remember user memory:
// <fact>”, “update project memory <note.md>: <revision>”, and “forget project
// memory <note.md>”, with either explicit scope where indicated. Ordinary
// conversational uses of “remember” or “forget” are not write authorization.
func ParseRequest(text string) (Request, bool) {
	text = strings.TrimSpace(text)
	if len(text) > maxEvidence || strings.ContainsRune(text, 0) {
		return Request{}, false
	}
	if parts := forgetRequest.FindStringSubmatch(text); parts != nil && validName(parts[2]) {
		return Request{op: "forget", scope: strings.ToLower(parts[1]), target: parts[2]}, true
	}
	if parts := updateRequest.FindStringSubmatch(text); parts != nil && validName(parts[2]) {
		if evidence := strings.TrimSpace(text[len(parts[0]):]); evidence != "" {
			return Request{op: "update", scope: strings.ToLower(parts[1]), target: parts[2], evidence: evidence}, true
		}
	}
	if parts := rememberRequest.FindStringSubmatch(text); parts != nil {
		if evidence := strings.TrimSpace(text[len(parts[0]):]); evidence != "" {
			scope := strings.ToLower(parts[1])
			if scope == "" {
				scope = "project"
			}
			return Request{op: "remember", scope: scope, evidence: evidence}, true
		}
	}
	return Request{}, false
}

func validName(name string) bool {
	if !noteName.MatchString(name) || strings.EqualFold(name, "MEMORY.md") {
		return false
	}
	// DOS device basenames are reserved even when followed by an extension.
	base := strings.ToUpper(strings.TrimSuffix(name, ".md"))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	return !(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9')
}
