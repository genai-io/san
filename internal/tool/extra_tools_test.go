package tool

import (
	"testing"

	"github.com/genai-io/san/internal/core"
)

func schemasContain(schemas []core.ToolSchema, name string) bool {
	for _, s := range schemas {
		if s.Name == name {
			return true
		}
	}
	return false
}

// TestExtraToolsPlumbing confirms caller-supplied schemas are appended to the
// set (this is how the main agent injects the Evolve trigger), absent by
// default, and subject to the same disabled filtering as built-in tools.
func TestExtraToolsPlumbing(t *testing.T) {
	extra := core.ToolSchema{Name: "Evolve", Description: "extra"}

	if schemasContain(GetToolSchemasWith(SchemaOptions{}), "Evolve") {
		t.Fatal("no extra tool supplied — Evolve must be absent from the default schema set")
	}
	if !schemasContain(GetToolSchemasWith(SchemaOptions{ExtraTools: []core.ToolSchema{extra}}), "Evolve") {
		t.Fatal("a supplied extra tool must appear in the schema set")
	}

	if schemasContain((&Set{}).Tools(), "Evolve") {
		t.Fatal("Evolve must be absent from a default Set")
	}
	if !schemasContain((&Set{ExtraTools: []core.ToolSchema{extra}}).Tools(), "Evolve") {
		t.Fatal("a Set's extra tools must appear in its toolset")
	}
	disabled := &Set{
		ExtraTools: []core.ToolSchema{extra},
		Disabled:   map[string]bool{"Evolve": true},
	}
	if schemasContain(disabled.Tools(), "Evolve") {
		t.Fatal("extra tools must pass through the disabled filter like any other tool")
	}
}
