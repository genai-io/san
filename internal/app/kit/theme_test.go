package kit

import (
	"math"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestThemeContrast(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resolve func(AdaptiveColor) string
	}{
		{"light", func(c AdaptiveColor) string { return c.Light }},
		{"dark", func(c AdaptiveColor) string { return c.Dark }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			theme := CurrentTheme
			for _, pair := range []struct {
				name string
				fg   AdaptiveColor
				bg   AdaptiveColor
			}{
				{"focus on terminal", theme.Focus, theme.Background},
				{"focus on input background", theme.Focus, theme.CommandBg},
				{"active tab text", TabActiveFg, TabActiveBg},
				{"startup wordmark", theme.Text, theme.Background},
				{"startup metadata", theme.Muted, theme.Background},
			} {
				if ratio := contrastRatio(tc.resolve(pair.fg), tc.resolve(pair.bg)); ratio < 4.5 {
					t.Errorf("%s contrast %.2f:1 is below 4.5:1", pair.name, ratio)
				}
			}
		})
	}
}

func contrastRatio(a, b string) float64 {
	luminance := func(hex string) float64 {
		linear := func(raw uint32) float64 {
			c := float64(raw) / 65535
			if c <= 0.04045 {
				return c / 12.92
			}
			return math.Pow((c+0.055)/1.055, 2.4)
		}
		r, g, b, _ := lipgloss.Color(hex).RGBA()
		return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
	}
	x, y := luminance(a), luminance(b)
	return (max(x, y) + 0.05) / (min(x, y) + 0.05)
}
