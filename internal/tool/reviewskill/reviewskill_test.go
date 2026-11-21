package reviewskill

import (
	"reflect"
	"strings"
	"testing"
)

func TestReviewSkillTriggerContract(t *testing.T) {
	trigger := &ReviewSkillTool{}
	schema := trigger.Schema()
	if trigger.Name() != "ReviewSkill" || schema.Name != trigger.Name() {
		t.Fatalf("unexpected trigger name: %q (schema %q)", trigger.Name(), schema.Name)
	}
	wantDefinition := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{
				"type": "string", "description": "One short clause naming the reusable technique or the existing skill issue to review.",
			},
		},
	}
	if !reflect.DeepEqual(schema.Definition, wantDefinition) {
		t.Fatalf("expected a single optional string argument, got %#v", schema.Definition)
	}

	for _, reason := range []string{"", "reusable test setup"} {
		var params map[string]any
		if reason != "" {
			params = map[string]any{"reason": reason}
		}
		result := trigger.Execute(t.Context(), params, "")
		want := "Requested Auto Skill review after this turn completes successfully."
		if reason != "" {
			want = strings.TrimSuffix(want, ".") + ": " + reason
		}
		if !result.Success || result.Output != want || result.Metadata.Title != trigger.Name() || result.Metadata.Icon != trigger.Icon() {
			t.Errorf("unexpected acknowledgement: %+v", result)
		}
	}
}
