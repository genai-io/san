package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestACPDoesNotDependOnCLI(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", repoModule+"/internal/app/acp").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == repoModule+"/internal/app/cli" || strings.HasPrefix(dep, repoModule+"/internal/app/cli/") {
			t.Errorf("ACP depends on %s", dep)
		}
	}
}

// Noninteractive entry points must not pull in the terminal runtime, even
// indirectly through shared app packages. Layer ranks alone cannot catch this.
func TestNonTerminalPackagesDoNotDependOnTUI(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", repoModule+"/internal/app/acp", repoModule+"/internal/app/cli", repoModule+"/internal/app/ask").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, root := range []string{repoModule + "/internal/app/tui", "charm.land/bubbletea"} {
			if dep == root || strings.HasPrefix(dep, root+"/") {
				t.Errorf("nonterminal app packages depend on terminal package %s", dep)
			}
		}
	}
}
