package app

import (
	"strings"

	"github.com/genai-io/san/internal/app/kit/suggest"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/workflow"
)

func commandSuggestionMatcher(cmdSvc *command.Registry, toolSvc *tool.Registry) func(string) []suggest.Suggestion {
	return func(query string) []suggest.Suggestion {
		// Dynamic branches depend on the current group or saved definitions.
		if name, args, ok := strings.Cut(strings.TrimPrefix(query, "/"), " "); ok {
			switch strings.ToLower(name) {
			case "group":
				return groupSuggestions(args)
			case "workflow":
				if suggestions, handled := workflowNameSuggestions(toolSvc, args); handled {
					return suggestions
				}
			}
		}
		cmds := cmdSvc.Matching(query)
		result := make([]suggest.Suggestion, len(cmds))
		for i, c := range cmds {
			result[i] = suggest.Suggestion{Name: c.Name, Description: c.Description}
		}
		return result
	}
}

func workflowNameSuggestions(toolSvc *tool.Registry, args string) ([]suggest.Suggestion, bool) {
	verb, rest, hasName := strings.Cut(args, " ")
	verb = strings.ToLower(verb)
	if !hasName || (verb != "show" && verb != "run") {
		return nil, false
	}
	if toolSvc == nil {
		return nil, true
	}
	t, ok := toolSvc.Get(tool.ToolWorkflow)
	if !ok {
		return nil, true
	}
	lister, ok := t.(interface {
		SavedDefinitions() []workflow.Saved
		SavedInputNames(string) ([]string, error)
	})
	if !ok {
		return nil, true
	}
	name, argsAfterName, hasInput := strings.Cut(rest, " ")
	if hasInput {
		if verb == "show" {
			return nil, true
		}
		return workflowInputSuggestions(lister, name, argsAfterName), true
	}
	var out []suggest.Suggestion
	for _, definition := range lister.SavedDefinitions() {
		if strings.HasPrefix(strings.ToLower(definition.Name), strings.ToLower(rest)) {
			out = append(out, suggest.Suggestion{
				Name: "workflow " + verb + " " + definition.Name, Description: definition.Description,
			})
		}
	}
	return out, true
}

func workflowInputSuggestions(lister interface {
	SavedInputNames(string) ([]string, error)
}, name, args string) []suggest.Suggestion {
	keys, err := lister.SavedInputNames(name)
	if err != nil {
		return nil
	}
	at := strings.LastIndex(args, " ")
	prefix, fragment := "", args
	if at >= 0 {
		prefix, fragment = args[:at+1], args[at+1:]
	}
	if strings.Contains(fragment, "=") {
		return nil // a value is free-form
	}
	used := make(map[string]bool)
	for _, field := range strings.Fields(prefix) {
		if key, _, ok := strings.Cut(field, "="); ok {
			used[key] = true
		}
	}
	var out []suggest.Suggestion
	for _, key := range keys {
		if !used[key] && strings.HasPrefix(key, fragment) {
			out = append(out, suggest.Suggestion{Name: "workflow run " + name + " " + prefix + key + "=", Description: "Value for {{input." + key + "}}"})
		}
	}
	return out
}
