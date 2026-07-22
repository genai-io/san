package tool

import "testing"

func TestFormatBashProgress(t *testing.T) {
	cases := []struct {
		lines int
		bytes int64
		want  string
	}{
		{0, 0, ""},
		{1, 6, "1 line"},
		{42, 900, "42 lines"},
		{1500, 40000, "1.5k lines"},
		{0, 200, "200 B"},
		{0, 2048, "2.0 KB"},
		{0, 5 * 1024 * 1024, "5.0 MB"},
	}
	for _, c := range cases {
		if got := formatBashProgress(c.lines, c.bytes); got != c.want {
			t.Errorf("formatBashProgress(%d, %d) = %q, want %q", c.lines, c.bytes, got, c.want)
		}
	}
}
