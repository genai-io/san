package transcript

import (
	"encoding/json"
	"reflect"

	"github.com/genai-io/san/internal/todo"
)

func (p StateRecord) apply(s *State) {
	if p.Title != nil {
		s.Title = *p.Title
	}
	if p.LastPrompt != nil {
		s.LastPrompt = *p.LastPrompt
	}
	if p.Mode != nil {
		s.Mode = *p.Mode
	}
	if p.AutoPilot != nil {
		s.AutoPilot = *p.AutoPilot
	}
	if p.Group != nil {
		s.Group = *p.Group
	}
	if p.Tasks != nil {
		s.Tasks = *p.Tasks
	}
}

// changes drops unchanged fields and owns the task payload retained in the cache.
func (p StateRecord) changes(s State) (*StateRecord, error) {
	if p.Title != nil && *p.Title == s.Title {
		p.Title = nil
	}
	if p.LastPrompt != nil && *p.LastPrompt == s.LastPrompt {
		p.LastPrompt = nil
	}
	if p.Mode != nil && *p.Mode == s.Mode {
		p.Mode = nil
	}
	if p.AutoPilot != nil && *p.AutoPilot == s.AutoPilot {
		p.AutoPilot = nil
	}
	if p.Group != nil && *p.Group == s.Group {
		p.Group = nil
	}
	if p.Tasks != nil {
		// Metadata may contain nested application values; own their durable JSON form.
		data, err := json.Marshal(*p.Tasks)
		if err != nil {
			return nil, err
		}
		tasks := new([]todo.Item)
		if err := json.Unmarshal(data, tasks); err != nil {
			return nil, err
		}
		if len(*tasks) == 0 && len(s.Tasks) == 0 || reflect.DeepEqual(*tasks, s.Tasks) {
			p.Tasks = nil
		} else {
			if *tasks == nil {
				*tasks = []todo.Item{}
			}
			p.Tasks = tasks
		}
	}
	if p.Title == nil && p.LastPrompt == nil && p.Mode == nil && p.AutoPilot == nil && p.Group == nil && p.Tasks == nil {
		return nil, nil
	}
	return &p, nil
}
