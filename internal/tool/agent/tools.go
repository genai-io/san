package agent

import (
	"fmt"
	"regexp"
)

const exactToolNamePattern = `^[a-zA-Z0-9_-]+$`

var exactToolName = regexp.MustCompile(exactToolNamePattern)

func parseAgentTools(params map[string]any) ([]string, error) {
	raw, present := params["tools"]
	if !present {
		return nil, nil
	}
	var names []string
	switch list := raw.(type) {
	case []string:
		if list != nil {
			names = append([]string{}, list...)
		}
	case []any:
		if list != nil {
			names = make([]string, len(list))
			for i, item := range list {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("tools[%d] must be an exact tool name", i)
				}
				names[i] = s
			}
		}
	}
	if names == nil {
		return nil, fmt.Errorf("tools must be an array of exact tool names; omit it to use the configured tools")
	}
	seen := make(map[string]bool, len(names))
	for i, s := range names {
		if !exactToolName.MatchString(s) {
			return nil, fmt.Errorf("tools[%d] must be an exact tool name", i)
		}
		if seen[s] {
			return nil, fmt.Errorf("duplicate tool name %q", s)
		}
		seen[s] = true
	}
	return names, nil
}
