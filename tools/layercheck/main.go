// Command layercheck verifies that the repository's internal package imports
// obey the layer ordering documented in docs/reference/dependency-rules.md.
//
// Layers (top of stack → bottom):
//
//	cmd  →  app  →  feature  →  core  →  infrastructure
//
// A higher layer may import a lower layer; the reverse is forbidden. Same-layer
// imports are allowed.
//
// Run:
//
//	go run ./tools/layercheck
//
// Exits 0 on success, 1 on violations, 2 on tool error.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const repoModule = "github.com/genai-io/gen-code"

// layerOf assigns each package path (relative to repoModule) to a layer.
// Subpackages without an explicit entry inherit from their nearest ancestor.
// Keep this map in sync with docs/reference/package-map.md.
var layerOf = map[string]string{
	"cmd/gen": "cmd",

	"internal/app": "app",

	"internal/core": "core",

	"internal/agent":       "feature",
	"internal/command":     "feature",
	"internal/cron":        "feature",
	"internal/hook":        "feature",
	"internal/identity":    "feature",
	"internal/image":       "feature", // touches core.Image; pure infra extraction tracked in notes/tech-debt.md
	"internal/inspector":   "feature",
	"internal/instruction": "feature",
	"internal/llm":         "feature",
	"internal/mcp":         "feature",
	"internal/plugin":      "feature",
	"internal/reminder":    "feature",
	"internal/search":      "feature",
	"internal/session":     "feature",
	"internal/setting":     "feature",
	"internal/skill":       "feature",
	"internal/subagent":    "feature",
	"internal/task":        "feature",
	"internal/tool":        "feature",
	"internal/worktree":    "feature",

	"internal/filecache": "infrastructure",
	"internal/log":       "infrastructure",
	"internal/markdown":  "infrastructure",
	"internal/proc":      "infrastructure",
	"internal/secret":    "infrastructure",
}

// rank orders layers from top of stack (0) to bottom (4). A package may import
// other packages of equal or greater rank, never lower rank.
var rank = map[string]int{
	"cmd":            0,
	"app":            1,
	"feature":        2,
	"core":           3,
	"infrastructure": 4,
}

func main() {
	pkgs, err := loadPackages()
	if err != nil {
		fmt.Fprintln(os.Stderr, "layercheck:", err)
		os.Exit(2)
	}

	type violation struct {
		from, fromLayer string
		to, toLayer     string
	}

	var violations []violation
	unknown := map[string]bool{}

	for _, p := range pkgs {
		fromRel, fromLayer, ok := lookupLayer(p.ImportPath)
		if !ok {
			continue // not one of ours, or unmapped
		}
		for _, imp := range p.Imports {
			toRel, toLayer, ok := lookupLayer(imp)
			if !ok {
				if strings.HasPrefix(imp, repoModule+"/") {
					unknown[strings.TrimPrefix(imp, repoModule+"/")] = true
				}
				continue
			}
			if rank[fromLayer] > rank[toLayer] {
				violations = append(violations, violation{fromRel, fromLayer, toRel, toLayer})
			}
		}
	}

	if len(unknown) > 0 {
		ks := make([]string, 0, len(unknown))
		for k := range unknown {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		fmt.Fprintln(os.Stderr, "layercheck: unmapped internal packages (add to layerOf):")
		for _, k := range ks {
			fmt.Fprintln(os.Stderr, "  "+k)
		}
		fmt.Fprintln(os.Stderr)
	}

	if len(violations) == 0 {
		fmt.Println("layercheck: no layer violations")
		if len(unknown) > 0 {
			os.Exit(2)
		}
		return
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].from != violations[j].from {
			return violations[i].from < violations[j].from
		}
		return violations[i].to < violations[j].to
	})

	fmt.Printf("layercheck: %d violation(s)\n", len(violations))
	for _, v := range violations {
		fmt.Printf("  %s (%s) -> %s (%s)\n", v.from, v.fromLayer, v.to, v.toLayer)
	}
	os.Exit(1)
}

// pkgInfo is the subset of `go list -json` output that we need.
type pkgInfo struct {
	ImportPath string
	Imports    []string
}

// loadPackages calls `go list -json ./internal/... ./cmd/...` and decodes the
// concatenated JSON stream into a slice. Test packages are excluded via the
// default behavior of `go list`; we don't enumerate test imports.
func loadPackages() ([]pkgInfo, error) {
	cmd := exec.Command("go", "list", "-json", "./internal/...", "./cmd/...")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("go list: %w\n%s", err, ee.Stderr)
		}
		return nil, fmt.Errorf("go list: %w", err)
	}

	dec := json.NewDecoder(strings.NewReader(string(out)))
	var pkgs []pkgInfo
	for dec.More() {
		var p pkgInfo
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// lookupLayer returns the relative path and layer assignment for an absolute
// import path under repoModule. Subpackages inherit their nearest ancestor's
// layer. Returns ok=false for paths outside the repo.
func lookupLayer(absPath string) (rel, layer string, ok bool) {
	r, ok := strings.CutPrefix(absPath, repoModule+"/")
	if !ok && absPath != repoModule {
		return "", "", false
	}
	rel = r
	walk := rel
	for {
		if l, ok := layerOf[walk]; ok {
			return rel, l, true
		}
		idx := strings.LastIndex(walk, "/")
		if idx < 0 {
			return "", "", false
		}
		walk = walk[:idx]
	}
}
