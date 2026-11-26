package input

import "testing"

func TestSideCommandsRunMidTurn(t *testing.T) {
	for in, want := range map[string]bool{"/btw why?": true, "/recap": true, "/group join shop": true, "/grouping": false, "/compact": false} {
		if got := IsSideCommand(in); got != want {
			t.Errorf("IsSideCommand(%q) = %v, want %v", in, got, want)
		}
	}
}
