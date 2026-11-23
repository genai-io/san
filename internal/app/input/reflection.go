package input

import "github.com/genai-io/san/internal/setting"

type ReflectionDeps struct {
	// Workspace supplies the current directory and settings after workspace reloads.
	Workspace func() (string, *setting.Settings)
	Learned   LearnedSkillStore
}

func NewReflectionSelector(deps ReflectionDeps) PanelPopup {
	return newPanelPopup("✦", "Reflection", "memory, skills & recap",
		&autoMemoryPanel{workspace: deps.Workspace},
		newAutoSkillPanel(deps.Workspace, deps.Learned),
		&recapPanel{workspace: deps.Workspace},
	)
}
