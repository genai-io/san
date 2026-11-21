package persona

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/genai-io/san/internal/setting"
)

// Settings is a persona's settings.json: a description, per-skill states, and
// a configuration overlay. The overlay reuses setting.Data so a persona can
// override the same fields the layered settings files do (permissions,
// disabled tools, model, …); the app layer applies it as the highest overlay
// while the persona is selected.
//
// Skills maps a skill name to "active" / "enable" / "disable". These states
// apply in-memory while the persona is selected and are never written to
// skills.json.
//
// Agents is an allow-list of subagent names: when non-empty, only those agent
// types are visible (spawnable + shown in the agents directory) while the
// persona is selected. Empty/omitted = all subagents visible. Like Skills, it
// is applied in-memory and never written to the agent enable/disable stores.
type Settings struct {
	Description string            `json:"description,omitempty"`
	Skills      map[string]string `json:"skills,omitempty"`
	Agents      []string          `json:"agents,omitempty"`

	// Embedded so the overlay fields (permissions, disabledTools, model, …)
	// sit at the top level of settings.json, e.g. {"disabledTools": {...}}.
	setting.Data
}

// UnmarshalJSON decodes both sets of top-level fields. The embedded Data
// decoder would otherwise consume the object and skip the persona fields.
func (s *Settings) UnmarshalJSON(data []byte) error {
	meta := struct {
		Description string            `json:"description"`
		Skills      map[string]string `json:"skills"`
		Agents      []string          `json:"agents"`
	}{s.Description, s.Skills, s.Agents}
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.Data); err != nil {
		return err
	}
	s.Description, s.Skills, s.Agents = meta.Description, meta.Skills, meta.Agents
	return nil
}

// parseSettings reads and parses a persona's settings.json. It returns
// (nil, false) when the file is missing or malformed — a persona with a broken
// settings.json still loads from its system/ and skills/ contents.
func parseSettings(path string) (*Settings, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, false
	}
	s.Description = strings.TrimSpace(s.Description)
	return &s, true
}
