package main

import "testing"

func TestParseLayerMap(t *testing.T) {
	md := `
| Path | Layer | Responsibility |
| --- | --- | --- |
| ` + "`cmd/san`" + ` | ` + "`cmd`" + ` | Main CLI binary. |
| ` + "`internal/app`" + ` | ` + "`app`" + ` | TUI shell. |
| ` + "`internal/core`" + ` | ` + "`core`" + ` | Shared contracts. |
| ` + "`internal/tool`" + ` | ` + "`feature`" + ` | Built-in tools. |
| ` + "`internal/infra/log`" + ` | ` + "`infrastructure`" + ` | Logging. |
`

	got, err := parseLayerMap(md)
	if err != nil {
		t.Fatalf("parseLayerMap() error = %v", err)
	}
	want := map[string]string{
		"cmd/san":            "cmd",
		"internal/app":       "app",
		"internal/core":      "core",
		"internal/tool":      "feature",
		"internal/infra/log": "infrastructure",
	}
	for pkg, layer := range want {
		if got[pkg] != layer {
			t.Fatalf("layer for %s = %q, want %q", pkg, got[pkg], layer)
		}
	}
}

func TestParseLayerMapRejectsUnknownLayer(t *testing.T) {
	_, err := parseLayerMap("| `internal/app` | `ui` | TUI shell. |")
	if err == nil {
		t.Fatal("parseLayerMap() error = nil, want error")
	}
}

func TestLookupLayerInheritsNearestAncestor(t *testing.T) {
	layerOf := map[string]string{
		"internal/app": "app",
	}
	rel, layer, ok := lookupLayer(layerOf, repoModule+"/internal/app/tui/input")
	if !ok {
		t.Fatal("lookupLayer() ok = false, want true")
	}
	if rel != "internal/app/tui/input" || layer != "app" {
		t.Fatalf("lookupLayer() = (%q, %q), want (internal/app/tui/input, app)", rel, layer)
	}
}
