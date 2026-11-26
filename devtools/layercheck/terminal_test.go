package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Layer ranks alone cannot catch transitive dependencies between hosts.
func TestNonTerminalDependencies(t *testing.T) {
	const app = repoModule + "/internal/app/"
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Deps " "}}`, app+"acp", app+"cli", app+"ask").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		host := fields[0]
		forbidden := []string{app + "tui", "charm.land/bubbletea"}
		if host == app+"acp" {
			forbidden = append(forbidden, app+"cli")
		}
		for _, dep := range fields[1:] {
			for _, root := range forbidden {
				if dep == root || strings.HasPrefix(dep, root+"/") {
					t.Errorf("%s depends on %s", host, dep)
				}
			}
		}
	}
}
