package skill

import (
	"os"
	"path/filepath"
	"strings"
)

// personaSkill is the registry record for one skill loaded by the active
// persona: the full name it occupies, plus the global skill (if any) that name
// displaced. ClearPersona uses it to remove the persona skill and restore what
// it shadowed.
type personaSkill struct {
	fullName string // registry key the persona skill occupies
	shadowed *Skill // global skill displaced at that key, or nil if it was free
}

// LoadPersona loads a persona's bundled skills (the SKILL.md in each directory)
// into the registry as an in-memory, active-by-default set, replacing any
// previously loaded persona skills. States from the persona's settings.json
// (skill name -> active/enable/disable) set each bundled skill's state; an
// unlisted skill defaults to active — selecting a persona activates its skills.
// Nothing is written to skills.json.
func (r *Registry) LoadPersona(skillDirs []string, states map[string]string) {
	bundled := parsePersonaSkills(skillDirs)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.clearPersonaLocked()
	var loaded []personaSkill
	for _, s := range bundled {
		fn := s.FullName()
		loaded = append(loaded, personaSkill{fullName: fn, shadowed: r.skills[fn]})
		s.State = bundledState(states, s)
		r.skills[fn] = s
	}
	r.personaSkills = loaded
}

// ClearPersona removes the active persona's skills and restores any global
// skills they shadowed. No-op when no persona skills are loaded.
func (r *Registry) ClearPersona() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearPersonaLocked()
}

func (r *Registry) clearPersonaLocked() {
	for _, ps := range r.personaSkills {
		if ps.shadowed != nil {
			r.skills[ps.fullName] = ps.shadowed
		} else {
			delete(r.skills, ps.fullName)
		}
	}
	r.personaSkills = nil
}

// parsePersonaSkills parses the SKILL.md in each directory into a *Skill,
// tagged ScopePersona. Directories without a readable SKILL.md are skipped.
func parsePersonaSkills(skillDirs []string) []*Skill {
	if len(skillDirs) == 0 {
		return nil
	}
	l := newLoader("")
	var out []*Skill
	for _, dir := range skillDirs {
		path := findSkillFile(dir)
		if path == "" {
			continue
		}
		s, err := l.loadSkillFile(path, ScopePersona, "")
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	return out
}

// findSkillFile returns the path to the SKILL.md inside dir (case-insensitive),
// or "" if there is none.
func findSkillFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(e.Name(), "SKILL.md") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// bundledState resolves a persona-bundled skill's state from the persona's
// settings.json skills map (keyed by full name or short name), defaulting to
// active.
func bundledState(states map[string]string, s *Skill) SkillState {
	for _, key := range []string{s.FullName(), s.Name} {
		if v, ok := states[key]; ok {
			return parseSkillState(v)
		}
	}
	return StateActive
}

func parseSkillState(v string) SkillState {
	switch SkillState(v) {
	case StateDisable, StateEnable, StateActive:
		return SkillState(v)
	default:
		return StateActive
	}
}
