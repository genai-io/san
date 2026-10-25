package toolresult

import "testing"

func TestLastLines(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		n           int
		want        string
	}{
		{"empty disabled", "", 0, ""},
		{"disabled", "output", 0, ""},
		{"negative", "output\n", -1, ""},
		{"empty", "", 3, ""},
		{"short", "one\ntwo\n", 3, "one\ntwo\n"},
		{"bounded", "one\ntwo\nthree\n", 2, "two\nthree\n"},
		{"unterminated", "one\ntwo\nthree", 1, "three"},
		{"blank interior", "one\n\nthree\n", 2, "\nthree\n"},
		{"blank tail", "one\ntwo\n\n\n", 1, "\n"},
		{"two blank tail lines", "one\ntwo\n\n\n", 2, "\n\n"},
		{"blank tail with text", "one\ntwo\n\n\n", 3, "two\n\n\n"},
		{"all blank", "\n\n\n", 1, "\n"},
		{"short all blank", "\n\n", 3, "\n\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := LastLines(tt.input, tt.n); got != tt.want {
				t.Fatalf("LastLines(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
			}
		})
	}
}
