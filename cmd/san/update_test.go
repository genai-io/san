package main

import (
	"os"
	"testing"
)

func TestConfirm(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"whatever\n", false},
		{"", false},
	}
	for _, tc := range tests {
		// Save and restore stdin
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		w.Write([]byte(tc.input))
		w.Close()
		os.Stdin = r

		got := confirm("test?")
		if got != tc.want {
			t.Errorf("confirm(%q) = %v, want %v", tc.input, got, tc.want)
		}
		os.Stdin = oldStdin
	}
}
