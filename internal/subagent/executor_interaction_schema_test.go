package subagent

import (
	"slices"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestInteractiveSchemasExposePromptsWithoutWideningReadonlyOrWhitelist(t *testing.T) {
	schemas := []core.ToolSchema{{Name: "Read"}, {Name: "Write"}, {Name: "Agent"}}
	for _, tc := range []struct {
		name        string
		mode        PermissionMode
		allow       ToolList
		interactive bool
		want        []core.ToolSchema
	}{
		{"headless default", PermissionDefault, nil, false, schemas[:1]},
		{"interactive default", PermissionDefault, nil, true, schemas[:2]},
		{"interactive readonly", PermissionExplore, nil, true, schemas[:1]},
		{"interactive constrained", PermissionDefault, ToolNames("Read"), true, schemas[:1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := filterSchemasForPermission(schemas, tc.mode, tc.allow, tc.interactive); !slices.Equal(got, tc.want) {
				t.Fatalf("schemas = %v, want %v", got, tc.want)
			}
		})
	}
}
