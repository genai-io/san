package toolresult

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBashDetailsShellLabelJSON(t *testing.T) {
	for _, label := range []string{"", "bash", "zsh", "pwsh", "PS"} {
		encoded, err := json.Marshal(BashDetails{ShellLabel: label})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "shellLabel") != (label != "") {
			t.Fatalf("unexpected JSON: %s", encoded)
		}
		var decoded BashDetails
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.ShellLabel != label {
			t.Fatalf("round trip: %+v", decoded)
		}
	}
	var historical BashDetails
	if err := json.Unmarshal([]byte(`{"lineCount":3,"tail":"old"}`), &historical); err != nil {
		t.Fatal(err)
	}
	if historical.ShellLabel != "" || historical.LineCount != 3 {
		t.Fatalf("historical=%+v", historical)
	}
}
