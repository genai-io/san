package agent

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/skill"
)

// prepareRecording keeps the runtime's fallback observer and native admissions
// on the same recorder. Headless construction uses it without a Runtime.
func (p *BuildParams) prepareRecording() error {
	if p.Recording == nil {
		return nil
	}
	if err := p.Recording.Err(); err != nil {
		return &llm.RecordingError{Err: err}
	}
	if p.SessionID != "" && p.SessionID != p.Recording.ID() {
		return fmt.Errorf("runtime and recording session IDs differ")
	}
	p.SessionID = p.Recording.ID()
	if p.recorder != nil {
		return nil
	}
	provider := ""
	if p.Provider != nil {
		provider = p.Provider.Name()
	}
	r := session.NewRecorder(p.Recording, provider)
	p.recorder = r
	observe := p.OnEvent
	p.OnEvent = func(event core.Event) {
		r.RecordEvent(event)
		if observe != nil {
			observe(event)
		}
	}
	return nil
}

func (p BuildParams) recordCatalogues(sys core.System, tools *core.Tools) error {
	if p.recorder == nil {
		return nil
	}
	var instructions []session.NamedInstruction
	for _, section := range sys.Sections() {
		if text := section.Render(); text != "" {
			instructions = append(instructions, session.NamedInstruction{Name: section.Name,
				InstructionSection: session.InstructionSection{Content: text, Slot: int(section.Slot)}})
		}
	}
	registry := p.Skills
	if registry == nil {
		registry = skill.DefaultIfInit()
	}
	states := map[string]string{}
	if registry != nil {
		for _, s := range registry.List() {
			state := session.SkillDisabled
			switch s.State {
			case skill.StateEnable:
				state = session.SkillEnabled
			case skill.StateActive:
				state = session.SkillActive
			}
			states[s.FullName()] = state
		}
	}
	cause := "boot"
	if p.resuming {
		cause = "resume"
	}
	if err := p.recorder.RecordCatalogues(context.Background(), instructions, tools.Schemas(), states, cause); err != nil {
		return &llm.RecordingError{Err: err}
	}
	return nil
}
