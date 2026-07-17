package evolve

import (
	"strings"
	"testing"
)

// TestCapabilitiesActive confirms the all-off zero value reads inactive (the
// app then omits the tool entirely) and any single capability activates it.
func TestCapabilitiesActive(t *testing.T) {
	if (Capabilities{}).Active() {
		t.Fatal("zero capabilities must be inactive")
	}
	for _, c := range []Capabilities{
		{CreateSkills: true}, {UpdateSkills: true}, {DeleteSkills: true}, {WriteMemory: true},
	} {
		if !c.Active() {
			t.Fatalf("%+v should be active", c)
		}
	}
}

// TestSchemaDescriptionIsDynamic confirms the description only advertises the
// enabled capabilities — memory-off never mentions memory, and skill-off never
// invites capturing/refining a skill.
func TestSchemaDescriptionIsDynamic(t *testing.T) {
	memOnly := Schema(Capabilities{WriteMemory: true}).Description
	if !strings.Contains(memOnly, "remembering") || strings.Contains(memOnly, "skill") {
		t.Fatalf("memory-only description should mention memory, not skills:\n%s", memOnly)
	}

	skillOnly := Schema(Capabilities{CreateSkills: true, UpdateSkills: true, DeleteSkills: true}).Description
	if strings.Contains(skillOnly, "remember") {
		t.Fatalf("skill-only description must not mention memory:\n%s", skillOnly)
	}
	if !strings.Contains(skillOnly, "new skill") {
		t.Fatalf("create-enabled description should mention capturing a new skill:\n%s", skillOnly)
	}

	// Delete-only (no create/update): no "new skill" invite, but the used-skill
	// line is present so the model can flag a skill worth retiring.
	delOnly := Schema(Capabilities{DeleteSkills: true}).Description
	if strings.Contains(delOnly, "new skill") {
		t.Fatalf("delete-only description must not invite creating skills:\n%s", delOnly)
	}
	if !strings.Contains(delOnly, "existing skill you used") {
		t.Fatalf("delete-only description should reference the used skill:\n%s", delOnly)
	}
}
