package moonshot

import "testing"

func TestStaticInputLimit(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"moonshot-v1-8k", 8_192},
		{"moonshot-v1-32k", 32_768},
		{"moonshot-v1-128k", 131_072},
		{"kimi-k2-0905-preview", 262_144},
		{"unknown-model", 0},
	}
	for _, c := range cases {
		if got := staticInputLimit(c.model); got != c.want {
			t.Errorf("staticInputLimit(%q) = %d, want %d", c.model, got, c.want)
		}
	}
}

func TestStaticOutputLimit(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"moonshot-v1-8k", 3_000},
		{"moonshot-v1-128k", 8_192},
		{"kimi-k2-0905-preview", 8_192},
		{"unknown-model", 0},
	}
	for _, c := range cases {
		if got := staticOutputLimit(c.model); got != c.want {
			t.Errorf("staticOutputLimit(%q) = %d, want %d", c.model, got, c.want)
		}
	}
}
