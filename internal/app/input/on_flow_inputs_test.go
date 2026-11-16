package input

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/task"
)

func TestFlowRunMissingInputsOpensSavedForm(t *testing.T) {
	for _, args := range []string{"run review", `run review base="   "`} {
		t.Run(args, func(t *testing.T) {
			c := flowController(t, map[string]string{"review": savedReview})
			c.env.Input, c.env.Height = &Model{}, 32
			before := len(task.Default().List())
			notice, _, err := c.handleFlowCommand(context.Background(), args)
			v := &c.env.Input.Flow
			if err != nil || notice != "" || !v.active || v.page != "inputs" || !v.saved || v.source != savedReview || v.baseline != savedReview || v.keys[v.field] != "base" {
				t.Fatalf("saved input form: notice=%q err=%v view=%+v", notice, err, v)
			}
			v.HandleKeypress(flowKey("esc"))
			v.HandleKeypress(flowKey("esc"))
			if v.active || len(task.Default().List()) != before {
				t.Fatal("opening or cancelling form created work")
			}
		})
	}
}

func TestFlowInputNavigationRetainsValues(t *testing.T) {
	s := flowUIFixture()
	s.source = flowDraft("sample", "work", "## work\nUse {{input.alpha}}, {{input.beta}}, {{input.gamma}}\n")
	c := NewSlashCommandController(SlashCommandEnv{Flow: s, Input: &Model{}, Width: 96, Height: 32})
	_, _, err := c.handleFlowCommand(context.Background(), `run sample alpha=supplied gamma=last extra=keep`)
	if err != nil {
		t.Fatal(err)
	}
	v := &c.env.Input.Flow
	if v.field != 1 || v.inputs["alpha"] != "supplied" || v.inputs["gamma"] != "last" || v.inputs["extra"] != "keep" {
		t.Fatal("supplied inputs not preserved/skipped")
	}
	v.HandleKeypress(flowKey("tab"))
	if v.field != 1 || v.message == "" {
		t.Fatal("blank Tab advanced")
	}
	v.HandlePaste("first")
	v.HandleKeypress(flowKey("shift+enter"))
	v.HandlePaste("second")
	v.HandleKeypress(flowKey("shift+tab"))
	if v.field != 0 || v.inputs["beta"] != "first\nsecond" || v.editor.Value() != "supplied" {
		t.Fatal("back navigation lost multiline edit")
	}
	v.editor.SetValue("changed")
	v.HandleKeypress(flowKey("esc"))
	if v.page != "review" || v.inputs["alpha"] != "changed" {
		t.Fatal("escape lost current edit")
	}
	v.HandleKeypress(flowKey("r"))
	if v.field != len(v.keys) || s.launches != 0 {
		t.Fatal("reopen failed to retain complete values")
	}
	before := v.editor.Value()
	v.HandlePaste("ignored")
	v.HandleKeypress(flowKey("x"))
	v.Update(tea.PasteMsg{Content: "ignored"})
	if v.editor.Value() != before || s.launches != 0 {
		t.Fatal("summary accepted editing")
	}
	v.HandleKeypress(flowKey("shift+tab"))
	if v.field != 2 || v.editor.Value() != "last" {
		t.Fatal("summary back did not edit last key")
	}
	v.editor.SetValue("updated last")
	v.HandleKeypress(flowKey("tab"))
	if v.field != len(v.keys) || s.launches != 0 {
		t.Fatal("Tab must only return to summary")
	}
	s.err = errors.New("launch unavailable")
	if cmd := v.HandleKeypress(flowKey("enter")); cmd != nil || !v.active || v.page != "inputs" || v.field != len(v.keys) || !strings.Contains(v.message, "launch unavailable") || v.inputs["gamma"] != "updated last" {
		t.Fatal("launch failure lost summary or values")
	}
	s.err = nil
	cmd := v.HandleKeypress(flowKey("enter"))
	if cmd == nil || v.active || s.launches != 2 || s.values["beta"] != "first\nsecond" || s.values["extra"] != "keep" {
		t.Fatal("explicit confirmation failed")
	}
	if _, ok := cmd().(FlowStartedMsg); !ok {
		t.Fatal("missing task animation signal")
	}
}

func TestFlowDirectRunWithUIStaysImmediate(t *testing.T) {
	for _, source := range []string{flowUISource, flowDraft("sample", "work", "## work\nDo the work\n")} {
		s := flowUIFixture()
		s.source = source
		ticks := 0
		c := NewSlashCommandController(SlashCommandEnv{Flow: s, Input: &Model{}, Width: 96, Height: 32, SpinnerTickCmd: func() tea.Cmd { ticks++; return func() tea.Msg { return "tick" } }})
		notice, cmd, err := c.handleFlowCommand(context.Background(), "run sample topic=supplied")
		if err != nil || s.launches != 1 || c.env.Input.Flow.active || ticks != 1 || cmd == nil || cmd() != "tick" || notice != "Started flow Sample · /flow stop task" {
			t.Fatalf("direct run changed: notice=%q err=%v", notice, err)
		}
	}
}

func TestFlowRunUIRejectsUnavailableOrInvalidDefinition(t *testing.T) {
	c := flowController(t, map[string]string{"broken": "not a flow"})
	c.env.Input = &Model{}
	before := len(task.Default().List())
	for _, args := range []string{"run missing", "run broken", "run broken malformed"} {
		if _, _, err := c.handleFlowCommand(context.Background(), args); err == nil {
			t.Fatalf("%s accepted", args)
		}
	}
	if c.env.Input.Flow.active || len(task.Default().List()) != before {
		t.Fatal("invalid command opened form or launched")
	}
}
