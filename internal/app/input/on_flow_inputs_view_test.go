package input

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestFlowInputsViewField(t *testing.T) {
	for _, size := range [][2]int{{96, 32}, {40, 18}, {24, 10}} {
		var v FlowView
		v.OpenDraft(flowUIFixture(), "sample", flowUISource, flowUISource, size[0], size[1])
		v.page, v.keys, v.field = "inputs", []string{"audit_scope"}, 0
		v.editor.SetWidth(max(1, min(72, size[0]-8)))
		v.editor.SetHeight(max(1, min(5, size[1]-13)))
		v.editor.SetValue("current edit")
		rendered := ansi.Strip(v.Render())
		wants := []string{"Audit scope", "current edit", "Enter next"}
		if size[0] >= 40 {
			wants = append(wants, "Esc review")
		}
		for _, want := range wants {
			if !strings.Contains(rendered, want) {
				t.Errorf("size %v: missing %q in %q", size, want, rendered)
			}
		}
		if strings.Contains(rendered, "{{") {
			t.Errorf("template jargon in form: %q", rendered)
		}
		for _, line := range strings.Split(rendered, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Errorf("size %v: line too wide: %q", size, line)
			}
		}
	}
}

func TestFlowInputsViewKeepsValidationAndEditorVisible(t *testing.T) {
	for _, height := range []int{8, 10, 18, 32} {
		var v FlowView
		v.OpenDraft(flowUIFixture(), "sample", flowUISource, flowUISource, 40, height)
		v.openInputs()
		v.editor.SetValue("current edit")
		v.message = "Enter a value to continue"
		rendered := ansi.Strip(v.Render())
		for _, want := range []string{"current edit", v.message, "Enter next", "Esc review"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("height %d: missing %q in %q", height, want, rendered)
			}
		}
		if len(strings.Split(rendered, "\n")) > height {
			t.Errorf("height %d: form exceeds screen", height)
		}
	}
}

func TestFlowInputsViewSummary(t *testing.T) {
	var v FlowView
	v.OpenDraft(flowUIFixture(), "sample", flowUISource, flowUISource, 40, 24)
	v.page, v.keys, v.field = "inputs", []string{"input", "audit-scope"}, 2
	v.inputs = map[string]string{"input": "Review the change", "audit-scope": "production\nstaging"}
	summary := ansi.Strip(v.renderInputSummary())
	for _, want := range []string{"Ready to run", "4 steps", "Request", "Review the change", "Audit scope", "production", "staging"} {
		if !strings.Contains(summary, want) {
			t.Errorf("missing %q in %q", want, summary)
		}
	}
	v.body.SetContent(v.renderInputSummary())
	rendered := ansi.Strip(v.Render())
	if !strings.Contains(rendered, "Enter run · Esc review") {
		t.Errorf("missing primary hints: %q", rendered)
	}
}
