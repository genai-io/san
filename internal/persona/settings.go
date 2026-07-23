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
type Settings struct {
	Description string            `json:"description,omitempty"`
	Skills      map[string]string `json:"skills,omitempty"`

	// Embedded so the overlay fields (permissions, disabledTools, model, …)
	// sit at the top level of settings.json, e.g. {"disabledTools": {...}}.
	setting.Data
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
