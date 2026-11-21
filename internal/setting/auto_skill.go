package setting

import (
	"encoding/json"
	"strings"

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
	d.migrateReviewSkill()
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
	d.migrateReviewSkill()
	return nil
}

const reviewSkillTool = "ReviewSkill"

func (d *Data) migrateReviewSkill() {
	const oldName = "Evolve"
	if disabled, ok := d.DisabledTools[oldName]; ok {
		if _, canonical := d.DisabledTools[reviewSkillTool]; !canonical {
			d.DisabledTools[reviewSkillTool] = disabled
		}
		delete(d.DisabledTools, oldName)
	}
	for _, rules := range [][]string{d.Permissions.Allow, d.Permissions.Deny, d.Permissions.Ask} {
		for i, rule := range rules {
			if rule == oldName || strings.HasPrefix(rule, oldName+"(") {
				rules[i] = reviewSkillTool + rule[len(oldName):]
			}
		}
	}
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
