package input

import "github.com/genai-io/san/internal/setting"

type ReflectionSources struct {
	// Workspace supplies the current directory and settings after workspace reloads.
	Workspace func() (string, *setting.Settings)
	Learned   LearnedSkillStore
}

func NewReflectionSelector(sources ReflectionSources) PanelPopup {
	return newPanelPopup("✦", "Reflection", "memory, skills & recap",
		&autoMemoryPanel{workspace: sources.Workspace},
		newAutoSkillPanel(sources.Workspace, sources.Learned),
		&recapPanel{workspace: sources.Workspace},
	)
}
