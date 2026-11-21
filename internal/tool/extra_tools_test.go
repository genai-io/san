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
// set (this is how the main agent injects the ReviewSkill trigger), absent by
// default, and subject to the same disabled filtering as built-in tools.
func TestExtraToolsPlumbing(t *testing.T) {
	extra := core.ToolSchema{Name: "ReviewSkill", Description: "extra"}

	if schemasContain(GetToolSchemasWith(SchemaOptions{}), "ReviewSkill") {
		t.Fatal("no extra tool supplied — ReviewSkill must be absent from the default schema set")
	}
	if !schemasContain(GetToolSchemasWith(SchemaOptions{ExtraTools: []core.ToolSchema{extra}}), "ReviewSkill") {
		t.Fatal("a supplied extra tool must appear in the schema set")
	}

	if schemasContain((&Set{}).Tools(), "ReviewSkill") {
		t.Fatal("ReviewSkill must be absent from a default Set")
	}
	if !schemasContain((&Set{ExtraTools: []core.ToolSchema{extra}}).Tools(), "ReviewSkill") {
		t.Fatal("a Set's extra tools must appear in its toolset")
	}
	disabled := &Set{
		ExtraTools: []core.ToolSchema{extra},
		Disabled:   map[string]bool{"ReviewSkill": true},
	}
	if schemasContain(disabled.Tools(), "ReviewSkill") {
		t.Fatal("extra tools must pass through the disabled filter like any other tool")
	}
}
