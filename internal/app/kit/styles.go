package kit

import (
	"charm.land/lipgloss/v2"
)

// Selector styles — lazy functions to pick up the current theme at render time.

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

// TabActiveBg is the background color for active tabs in tabbed panels.
var TabActiveBg = AdaptiveColor{Dark: "#4F6D9B", Light: "#3B6FC0"}

// TabActiveFg is the foreground color for active tabs in tabbed panels.
var TabActiveFg = AdaptiveColor{Dark: "#FFFFFF", Light: "#FFFFFF"}

// SearchBg is the background color for search/filter input boxes.
var SearchBg = AdaptiveColor{Dark: "#27272A", Light: "#E4E4E7"}
