// Imperative user-driven model actions that don't fit a single sub-feature:
// switching the active persona with a hot-patch of the running agent's prompt
// parts and skills, plus opening and deleting personas from the picker.
package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/setting"
)

// setActivePersona persists the persona choice and applies it without
// restarting the session. Immediate: the persona's skills swap in-memory and
// the running main agent's prompt parts (identity / behavior / rules) are
// hot-patched, both visible on the next inference. The persona's settings
// overlay (disabled tools, permissions) takes effect on the next agent
// rebuild. Empty name = no persona (built-in defaults).
func (m *model) setActivePersona(name string) error {
	if snap := m.services.Setting.Snapshot(); snap != nil && snap.Persona == name {
		return nil
	}
	// Persist the selection at a scope that actually wins the merge. Project
	// scope overrides user scope, so write project scope when the project
	// already pins a persona — otherwise that pin would shadow a user-scope
	// write and the switch would silently do nothing (e.g. switching away from
	// a project persona to the built-in default) — or when the target is itself
	// a project persona. Otherwise persist user-level.
	userLevel := true
	if p, ok := m.services.Persona.Get(name); ok && p.Scope == persona.ScopeProject {
		userLevel = false
	}
	if setting.PersonaAt(m.env.CWD, false) != "" {
		userLevel = false
	}
	if err := setting.SavePersonaAt(m.env.CWD, name, userLevel); err != nil {
		return err
	}
	_ = m.services.Setting.Reload(m.env.CWD)

	// Skills (immediate): swap the in-memory persona skill set, then re-emit
	// the skills-directory reminder so the model sees it on the next turn.
	m.applyPersonaSkills()
	m.services.Reminder.RequeueSystemReminders()

	// Prompt (immediate): hot-patch the running main agent's parts.
	if sys := m.services.Agent.System(); sys != nil {
		system.SwapPersona(sys, m.personaPrompt())
	}
	m.ReconfigureAgentTool()
	return nil
}

// openPersona opens the named persona's files for editing with the OS default
// handler — no $EDITOR / terminal editor required, and it doesn't take over the
// TUI. Opens the identity prompt if present, else settings.json, else the
// directory. The built-in default has no files to open.
func (m *model) openPersona(name string) tea.Cmd {
	p, ok := m.services.Persona.Get(name)
	if !ok || p.IsBuiltin() || p.Dir == "" {
		return nil
	}
	target, isFile := p.Dir, false
	for _, rel := range []string{filepath.Join("system", "identity.md"), "settings.json"} {
		cand := filepath.Join(p.Dir, rel)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			target, isFile = cand, true
			break
		}
	}
	return func() tea.Msg {
		_ = openInOS(target, isFile)
		return nil
	}
}

// openInOS launches the OS default handler for path without blocking the TUI:
// macOS `open` (`-t` = default text editor for files), Linux `xdg-open`,
// Windows `start`.
func openInOS(path string, isFile bool) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if isFile {
			cmd = exec.Command("open", "-t", path)
		} else {
			cmd = exec.Command("open", path)
		}
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// deletePersona removes a user/project persona's directory. If it was the
// active persona, the selection falls back to the built-in default first so the
// session never points at a directory that's about to disappear. The built-in
// default cannot be deleted.
func (m *model) deletePersona(name string) error {
	p, ok := m.services.Persona.Get(name)
	if !ok || p.IsBuiltin() || p.Dir == "" {
		return fmt.Errorf("cannot delete %q", name)
	}

	if snap := m.services.Setting.Snapshot(); snap != nil && snap.Persona == name {
		_ = m.setActivePersona(persona.DefaultName)
	}
	if err := os.RemoveAll(p.Dir); err != nil {
		return err
	}
	m.services.Persona.Reload()
	m.applyPersonaSkills()
	return nil
}
