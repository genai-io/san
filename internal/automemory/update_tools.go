package automemory

import (
	"strings"
	"unicode/utf8"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/markdown"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
)

const maxNoteEdits = 4

type updateTools struct {
	read, write, edit, forget bool
}

// Reuse the main file-tool schemas where available, without dispatching to the
// main agent's tool runtime. The scoped deletion schema is added separately.
func availableUpdateTools(req *ai.Request) (tools updateTools) {
	if req.ToolChoice != ai.ToolChoiceAuto {
		return tools
	}
	forgetNameTaken := false
	for _, schema := range req.Tools {
		switch schema.Name {
		case tool.ToolRead:
			tools.read = true
		case tool.ToolWrite:
			tools.write = true
		case tool.ToolEdit:
			tools.edit = true
		case toolForgetMemory:
			forgetNameTaken = true
		}
	}
	tools.forget = tools.read && !forgetNameTaken
	return tools
}

func (t updateTools) proposal(calls []ai.ToolCall) (proposal, error) {
	if t.read && calls[0].Name == tool.ToolRead {
		return readToolProposal(calls)
	}
	if t.edit && calls[0].Name == tool.ToolEdit {
		return editToolProposal(calls)
	}
	if len(calls) != 1 {
		return proposal{}, errInvalidProposal
	}
	call := calls[0]
	switch {
	case call.Name == tool.ToolWrite && t.write:
		return writeToolProposal(call.Input)
	case call.Name == toolForgetMemory && t.forget:
		return forgetToolProposal(call.Input)
	default:
		return proposal{}, errInvalidProposal
	}
}

func readToolProposal(calls []ai.ToolCall) (proposal, error) {
	if len(calls) > maxNotesPerRead {
		return proposal{}, errInvalidProposal
	}
	p := proposal{Action: actionRead}
	for i, call := range calls {
		var args struct {
			File string `json:"file_path"`
		}
		if call.Name != tool.ToolRead || call.ID == "" || (i > 0 && call.ID == calls[0].ID) ||
			!decodeUpdateInput(call.Input, &args) || !validName(args.File) {
			return proposal{}, errInvalidProposal
		}
		p.Files = append(p.Files, args.File)
	}
	return p, nil
}

func writeToolProposal(input string) (proposal, error) {
	var args struct {
		File    string `json:"file_path"`
		Content string `json:"content"`
	}
	if !decodeUpdateInput(input, &args) || !validName(args.File) {
		return proposal{}, errInvalidProposal
	}
	frontmatter, body, err := markdown.SplitFrontmatter(args.Content)
	if err != nil || frontmatter == "" {
		return proposal{}, errInvalidProposal
	}
	meta, err := parseNoteMetadata(frontmatter)
	if err != nil {
		return proposal{}, errInvalidProposal
	}
	return proposal{Action: actionRemember, File: args.File, Name: meta.Name, Description: meta.Description, Type: meta.Type, Body: body}, nil
}

func editToolProposal(calls []ai.ToolCall) (proposal, error) {
	if len(calls) > maxNoteEdits {
		return proposal{}, errInvalidProposal
	}
	p := proposal{Action: actionUpdate}
	for _, call := range calls {
		var args struct {
			File string `json:"file_path"`
			noteEdit
		}
		if call.Name != tool.ToolEdit || !decodeUpdateInput(call.Input, &args) || !validName(args.File) ||
			(p.File != "" && p.File != args.File) {
			return proposal{}, errInvalidProposal
		}
		p.File = args.File
		p.Edits = append(p.Edits, args.noteEdit)
	}
	return p, nil
}

func decodeUpdateInput(input string, args any) bool {
	return len(input) <= 16*1024 && utf8.ValidString(input) && core.DecodeStrictJSON(strings.NewReader(input), args) == nil
}
