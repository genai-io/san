// Imperative user-driven model actions that don't fit a single sub-feature:
// switching the active persona with a hot-patch of the running agent's prompt
// parts and skills, plus editing and deleting personas from the picker.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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
	if m.services.Setting != nil {
		if snap := m.services.Setting.Snapshot(); snap != nil && snap.Persona == name {
			return nil
		}
	}
	// Persist the selection at a scope that actually wins the merge. Project
	// scope overrides user scope, so write project scope when the project
	// already pins a persona — otherwise that pin would shadow a user-scope
	// write and the switch would silently do nothing (e.g. switching away from
	// a project persona to the built-in default) — or when the target is itself
	// a project persona. Otherwise persist user-level.
	userLevel := true
	if m.services.Persona != nil {
		if p, ok := m.services.Persona.Get(name); ok && p.Scope == persona.ScopeProject {
			userLevel = false
		}
	}
	if setting.PersonaAt(m.env.CWD, false) != "" {
		userLevel = false
	}
	if err := setting.SavePersonaAt(m.env.CWD, name, userLevel); err != nil {
		return err
	}
	if m.services.Setting != nil {
		_ = m.services.Setting.Reload(m.env.CWD)
	}

	// Skills (immediate): swap the in-memory persona skill set, then re-emit
	// the skills-directory reminder so the model sees it on the next turn.
	m.applyPersonaSkills()
	m.applyPersonaAgents()
	if m.services.Reminder != nil {
		m.services.Reminder.RequeueSystemReminders()
	}

	// Prompt (immediate): hot-patch the running main agent's parts.
	if m.services.Agent != nil {
		if sys := m.services.Agent.System(); sys != nil {
			provider := ""
			if m.env.LLMProvider != nil {
				provider = m.env.LLMProvider.Name()
			}
			system.SwapPersona(sys, m.personaPrompt(), m.env.IsGit, provider)
		}
	}
	m.ReconfigureAgentTool()
	return nil
}

// editPersona opens the named persona's files in $EDITOR — the identity prompt
// if present, else settings.json, else the directory. The built-in default has
// no files to edit.
func (m *model) editPersona(name string) tea.Cmd {
	if m.services.Persona == nil {
		return nil
	}
	p, ok := m.services.Persona.Get(name)
	if !ok || p.IsBuiltin() || p.Dir == "" {
		return nil
	}
	target := p.Dir
	for _, rel := range []string{filepath.Join("system", "identity.md"), "settings.json"} {
		cand := filepath.Join(p.Dir, rel)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			target = cand
			break
		}
	}
	return m.StartExternalEditor(target)
}

// deletePersona removes a user/project persona's directory. If it was the
// active persona, the selection falls back to the built-in default first so the
// session never points at a directory that's about to disappear. The built-in
// default cannot be deleted.
func (m *model) deletePersona(name string) error {
	if m.services.Persona == nil {
		return fmt.Errorf("persona registry unavailable")
	}
	p, ok := m.services.Persona.Get(name)
	if !ok || p.IsBuiltin() || p.Dir == "" {
		return fmt.Errorf("cannot delete %q", name)
	}

	if m.services.Setting != nil {
		if snap := m.services.Setting.Snapshot(); snap != nil && snap.Persona == name {
			_ = m.setActivePersona(persona.DefaultName)
		}
	}
	if err := os.RemoveAll(p.Dir); err != nil {
		return err
	}
	m.services.Persona.Reload()
	m.applyPersonaSkills()
	m.applyPersonaAgents()
	return nil
}

// createPersona scaffolds a new user-scope persona (system/identity.md +
// settings.json), switches to it, and opens the prompt in $EDITOR.
func (m *model) createPersona(name string) (tea.Cmd, error) {
	name = sanitizePersonaName(name)
	if name == "" {
		return nil, fmt.Errorf("invalid persona name")
	}
	if name == persona.DefaultName {
		return nil, fmt.Errorf("%q is reserved", name)
	}
	if m.services.Persona == nil {
		return nil, fmt.Errorf("persona registry unavailable")
	}
	if _, exists := m.services.Persona.Get(name); exists {
		return nil, fmt.Errorf("persona %q already exists", name)
	}

	root, err := persona.UserDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "system"), 0o755); err != nil {
		return nil, err
	}
	identity := filepath.Join(dir, "system", "identity.md")
	if err := os.WriteFile(identity, []byte("You are "+name+".\n"), 0o644); err != nil {
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{\n  \"description\": \"\"\n}\n"), 0o644)

	m.services.Persona.Reload()
	_ = m.setActivePersona(name)
	return m.StartExternalEditor(identity), nil
}

// sanitizePersonaName reduces a free-form name to a safe kebab-case directory
// name (lowercase letters, digits, single dashes; trimmed).
func sanitizePersonaName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ':
			if b.Len() > 0 && !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
