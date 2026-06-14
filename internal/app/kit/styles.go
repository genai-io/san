package kit

import (
	"charm.land/lipgloss/v2"
)

// Selector styles — lazy functions to pick up the current theme at render time.

// FocusBar is the thin vertical accent drawn to the left of a focused row.
// It is the one consistent selection affordance across every list surface
// (selectors, slash suggestions, approval, questions) — replacing the old mix
// of ">", "❯", and bare bold text.
const FocusBar = "▎"

// FocusBarStyle colors the FocusBar in the brand accent.
func FocusBarStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(CurrentTheme.Focus)
}

// SelectorSelectedLabelStyle styles the label of a focused row: bright + bold,
// with no padding so it composes after a separately-colored FocusBar.
func SelectorSelectedLabelStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.TextBright).
		Bold(true)
}

// SelectorItemLabelStyle is the unselected row label without the PaddingLeft
// that SelectorItemStyle carries — used by the width-padded panel rows that
// manage their own indent.
func SelectorItemLabelStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.Text)
}

func SelectorBorderStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(CurrentTheme.Primary).
		Padding(1, 2)
}

func SelectorTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.Primary).
		Bold(true)
}

func SelectorItemStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.Text).
		PaddingLeft(2)
}

func SelectorSelectedStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.TextBright).
		Bold(true).
		PaddingLeft(2)
}

func SelectorStatusConnected() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.Success)
}

func SelectorStatusReady() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.Warning)
}

func SelectorStatusNone() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.TextDim)
}

func SelectorStatusError() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.Error)
}

func SelectorHintStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.TextDim).
		MarginTop(1)
}

func SelectorBreadcrumbStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.Text).
		MarginBottom(1)
}

// DimStyle is a plain dim-text style (no margins/padding).
func DimStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(CurrentTheme.TextDim)
}

// TabActiveBg is the background of the active tab pill — the brand teal, so
// "active tab" and the selected-row FocusBar share one accent.
var TabActiveBg = AdaptiveColor{Dark: "#46E8C0", Light: "#0D9488"}

// TabActiveFg is the active-tab text: dark ink on the bright dark-mode teal,
// white on the deeper light-mode teal, so contrast holds either way.
var TabActiveFg = AdaptiveColor{Dark: "#18181B", Light: "#FFFFFF"}

// SearchBg is the background color for search/filter input boxes.
var SearchBg = AdaptiveColor{Dark: "#27272A", Light: "#E4E4E7"}
