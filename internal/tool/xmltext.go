package tool

import "strings"

// xmlBodyEscaper escapes only the three characters that would let a body break
// out of an XML-ish envelope tag. Unlike encoding/xml.EscapeText it leaves
// newlines and tabs intact, so multi-line agent output and interim messages
// stay readable when wrapped in an <agent-message> or <task-notification>
// block instead of turning every line break into a "&#xA;" entity.
var xmlBodyEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// EscapeXMLText escapes &, <, and > in s for safe inclusion inside an XML-ish
// envelope tag, preserving every other character (notably newlines and tabs).
func EscapeXMLText(s string) string {
	return xmlBodyEscaper.Replace(s)
}
