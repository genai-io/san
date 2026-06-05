// Package kit provides utility functions and styles used across UI packages.
package kit

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/san/v2/internal/secret"
)

func FuzzyMatch(str, pattern string) bool {
	pi := 0
	for si := 0; si < len(str) && pi < len(pattern); si++ {
		if str[si] == pattern[pi] {
			pi++
		}
	}
	return pi == len(pattern)
}

func CalculateBoxWidth(screenWidth int) int {
	boxWidth := screenWidth - 8
	return max(40, min(boxWidth, 60))
}

func CalculateToolBoxWidth(screenWidth int) int {
	boxWidth := screenWidth * 80 / 100
	return max(60, boxWidth)
}

// TruncateText shortens text to maxLen with ellipsis if needed.
// Returns the original text if maxLen <= 0 or if text fits within maxLen.
// Uses rune-based slicing to avoid breaking multi-byte characters.
func TruncateText(text string, maxLen int) string {
	runes := []rune(text)
	if maxLen <= 0 || len(runes) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func ShortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func ShortenPathForProject(path, cwd string) string {
	if strings.HasPrefix(path, cwd) {
		rel := strings.TrimPrefix(path, cwd)
		rel = strings.TrimPrefix(rel, "/")
		if rel != "" {
			return rel
		}
	}
	return ShortenPath(path)
}

// RenderSelectableRow renders a row with "> " or "  " prefix.
func RenderSelectableRow(line string, isSelected bool) string {
	if isSelected {
		return SelectorSelectedStyle().Render("> " + line)
	}
	return SelectorItemStyle().Render("  " + line)
}

// alignedRowMinGap is the minimum spacing kept between the name and info
// columns, so names longer than colWidth never collide with the info column.
const alignedRowMinGap = 2

// FormatAlignedRow formats "icon  name<padding>info" with name padded to
// colWidth and always separated from info by at least alignedRowMinGap spaces.
func FormatAlignedRow(icon, name string, colWidth int, info string) string {
	gap := colWidth - lipgloss.Width(name) // display width, ANSI/Unicode safe
	if gap < alignedRowMinGap {
		gap = alignedRowMinGap
	}
	return fmt.Sprintf("%s  %s%s%s", icon, name, strings.Repeat(" ", gap), info)
}

// MapString extracts a string value from a generic map.
func MapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

// MapInt extracts an int value from a generic map.
func MapInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	value, ok := m[key]
	if !ok || value == nil {
		return 0
	}
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// RenderEnvVarStatus returns a styled "ENVVAR ✓" or "ENVVAR ✗" indicator.
func RenderEnvVarStatus(envVar string) string {
	if envVar == "" {
		return ""
	}
	// The env-var name is secondary reference info (kept dim); the check mark
	// carries the signal — green ✓ when configured, dim ✗ when not.
	name := DimStyle().Render(envVar)
	if secret.Resolve(envVar) != "" {
		return name + " " + SelectorStatusConnected().Render("✓")
	}
	return name + " " + SelectorStatusNone().Render("✗")
}
