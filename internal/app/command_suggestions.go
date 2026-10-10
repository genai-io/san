package app

import (
	"strings"
	"unicode"

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
	fields, _ := command.ScanArguments(args)
	if len(fields) == 0 {
		return nil, false
	}
	verb := strings.ToLower(fields[0].Value)
	if (len(fields) == 1 && fields[0].End == len(args)) || (verb != "show" && verb != "run") {
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
	name := ""
	if len(fields) > 1 {
		name = fields[1].Value
	}
	if len(fields) > 1 && fields[1].End < len(args) {
		if verb == "show" {
			return nil, true
		}
		argsAfterName := strings.TrimLeftFunc(args[fields[1].End:], unicode.IsSpace)
		return workflowInputSuggestions(lister, name, argsAfterName), true
	}
	var out []suggest.Suggestion
	for _, definition := range lister.SavedDefinitions() {
		if strings.HasPrefix(strings.ToLower(definition.Name), strings.ToLower(name)) {
			out = append(out, suggest.Suggestion{
				Name: "workflow " + verb + " " + command.QuoteArgument(definition.Name), Description: definition.Description,
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
	fields, err := command.ScanArguments(args)
	if err != nil {
		return nil // still inside a quoted value
	}
	prefix, fragment := args, ""
	if len(fields) > 0 && fields[len(fields)-1].End == len(args) {
		last := fields[len(fields)-1]
		prefix, fragment = args[:last.Start], last.Value
		fields = fields[:len(fields)-1]
	}
	if strings.Contains(fragment, "=") {
		return nil // a value is free-form
	}
	used := make(map[string]bool)
	for _, field := range fields {
		if key, _, ok := strings.Cut(field.Value, "="); ok {
			used[key] = true
		}
	}
	var out []suggest.Suggestion
	for _, key := range keys {
		if !used[key] && strings.HasPrefix(key, fragment) {
			out = append(out, suggest.Suggestion{Name: "workflow run " + command.QuoteArgument(name) + " " + prefix + key + "=", Description: "Value for {{input." + key + "}}"})
		}
	}
	return out
}
