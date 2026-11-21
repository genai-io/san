package setting

import (
	"encoding/json"
	"strings"

	"github.com/genai-io/san/internal/core"
	"gopkg.in/yaml.v3"
)

// dataWire avoids recursion when decoding the rest of Data. Resolve the
// spelling in each document before merging settings across layers.
type dataWire Data

func (d *Data) UnmarshalJSON(data []byte) error {
	wire := struct {
		*dataWire
		Canonical json.RawMessage `json:"autoSkill"`
		Legacy    json.RawMessage `json:"selfLearn"`
	}{dataWire: (*dataWire)(d)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	block := wire.Canonical
	if block == nil {
		block = wire.Legacy
	}
	if block != nil {
		if err := json.Unmarshal(block, &d.AutoSkill); err != nil {
			return err
		}
	}
	return nil
}

func (d *Data) UnmarshalYAML(node *yaml.Node) error {
	if err := node.Decode((*dataWire)(d)); err != nil {
		return err
	}
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return err
	}
	if _, canonical := fields["autoSkill"]; !canonical {
		if legacy, ok := fields["selfLearn"]; ok {
			if err := legacy.Decode(&d.AutoSkill); err != nil {
				return err
			}
		}
	}
	return nil
}

const reviewSkillTool = core.ReviewSkillToolName

// AutoSkillDisabledByEnv reports whether the legacy hard-disable switch is set.
func AutoSkillDisabledByEnv() bool {
	v := Getenv("DISABLE_SELF_LEARN")
	return v == "1" || strings.EqualFold(v, "true")
}

// ReviewSkillEnabled reports whether the review trigger is enabled in settings.
func (s *Settings) ReviewSkillEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data != nil {
		if off, ok := s.data.DisabledTools[reviewSkillTool]; ok {
			return !off
		}
	}
	return !IsDefaultDisabledTool(reviewSkillTool)
}

func (s AutoSkillPermissions) MarshalYAML() (any, error) {
	return s.wire(), nil
}

func (s *AutoSkillPermissions) UnmarshalYAML(node *yaml.Node) error {
	var wire wireSkills
	if err := node.Decode(&wire); err != nil {
		return err
	}
	s.fromWire(wire)
	return nil
}
