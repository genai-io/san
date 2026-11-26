package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/infra/markdown"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
)

const maxChangeCalls = 4

type updateTools struct {
	read, write, edit bool
	shells            []string
}

func availableUpdateTools(req *ai.Request) (tools updateTools) {
	if req.ToolChoice != ai.ToolChoiceAuto {
		return tools
	}
	for _, schema := range req.Tools {
		switch schema.Name {
		case tool.ToolRead:
			tools.read = true
		case tool.ToolWrite:
			tools.write = true
		case tool.ToolEdit:
			tools.edit = true
		case tool.ToolBash, tool.ToolPowerShell:
			tools.shells = append(tools.shells, schema.Name)
		}
	}
	return tools
}

func (t updateTools) proposals(calls []ai.ToolCall, dir string, notes []noteRevision) ([]proposal, error) {
	if calls[0].Name == tool.ToolRead || tool.IsShellTool(calls[0].Name) {
		p, err := t.proposal(calls, dir, notes)
		return []proposal{p}, err
	}
	if len(calls) > maxChangeCalls {
		return nil, errInvalidProposal
	}
	var changes []proposal
	positions := make(map[string]int, len(calls))
	for _, call := range calls {
		if call.Name != tool.ToolWrite && call.Name != tool.ToolEdit {
			return nil, errInvalidProposal
		}
		p, err := t.proposal([]ai.ToolCall{call}, dir, notes)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(p.File)
		if i, ok := positions[key]; ok {
			prior := &changes[i]
			if prior.File != p.File || prior.Action != actionUpdate || p.Action != actionUpdate {
				return nil, errInvalidProposal
			}
			prior.Params = append(prior.Params, p.Params...)
			continue
		}
		positions[key] = len(changes)
		changes = append(changes, p)
	}
	return changes, nil
}

func (t updateTools) proposal(calls []ai.ToolCall, dir string, notes []noteRevision) (proposal, error) {
	var p proposal
	name := calls[0].Name
	var maxCalls int
	var fields []string
	switch {
	case tool.IsShellTool(name) && len(calls) == 1:
		return forgetProposalFor(calls[0], dir, notes, t.shells)
	case name == tool.ToolRead && t.read:
		p.Action, maxCalls = actionRead, maxNotesPerRead
	case name == tool.ToolWrite && t.write:
		p.Action, maxCalls, fields = actionRemember, 1, []string{"content"}
	case name == tool.ToolEdit && t.edit:
		p.Action, maxCalls, fields = actionUpdate, 1, []string{"old_string", "new_string", "replace_all"}
	default:
		return proposal{}, errInvalidProposal
	}
	if len(calls) > maxCalls {
		return proposal{}, errInvalidProposal
	}
	for i, call := range calls {
		var args map[string]any
		if call.Name != name || !decodeUpdateInput(call.Input, &args) {
			return proposal{}, errInvalidProposal
		}
		file, _ := args["file_path"].(string)
		if filepath.IsAbs(file) {
			if filepath.Dir(file) != dir {
				return proposal{}, errInvalidProposal
			}
			file = filepath.Base(file)
		}
		indexEdit := p.Action == actionUpdate && file == system.AutoMemoryIndexName
		indexRead := p.Action == actionRead && file == system.AutoMemoryIndexName
		if (!indexRead && !indexEdit && !validName(file)) || (p.File != "" && p.File != file) {
			return proposal{}, errInvalidProposal
		}
		for field := range args {
			if field == "file_path" {
				continue
			}
			if !slices.Contains(fields, field) {
				return proposal{}, errInvalidProposal
			}
		}
		args["file_path"] = file
		switch p.Action {
		case actionRead:
			if call.ID == "" || (i > 0 && call.ID == calls[0].ID) {
				return proposal{}, errInvalidProposal
			}
			p.Files = append(p.Files, file)
		case actionRemember:
			if content, ok := args["content"].(string); !ok || !noteText(content) {
				return proposal{}, errInvalidProposal
			}
			p.File, p.Params = file, append(p.Params, args)
		case actionUpdate:
			if all, present := args["replace_all"]; present {
				if enabled, ok := all.(bool); !ok || (indexEdit && enabled) {
					return proposal{}, errInvalidProposal
				}
			}
			p.File, p.Params = file, append(p.Params, args)
		}
	}
	return p, nil
}

func decodeUpdateInput(input string, args any) bool {
	return len(input) <= 16*1024 && utf8.ValidString(input) && core.DecodeStrictJSON(strings.NewReader(input), args) == nil
}

// updateFiles runs the existing file tools against private copies. The memory
// store owns freshness checks and decides whether to commit the resulting bytes.
type updateFiles struct {
	dir   string
	views *fs.FileViews
}

func (f *updateFiles) open() error {
	if f.dir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "san-memory-")
	if err == nil {
		f.dir, f.views = dir, fs.NewFileViews()
	}
	return err
}

func (f *updateFiles) close() {
	if f.dir != "" {
		_ = os.RemoveAll(f.dir)
	}
}

func (f *updateFiles) run(ctx context.Context, name string, args map[string]any) (toolresult.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return toolresult.ToolResult{}, err
	}
	var impl tool.Tool
	switch name {
	case tool.ToolRead:
		impl = &fs.ReadTool{}
	case tool.ToolWrite:
		impl = &fs.WriteTool{}
	case tool.ToolEdit:
		impl = &fs.EditTool{}
	case tool.ToolBash, tool.ToolPowerShell:
		registered, _ := tool.Get(name)
		shell, ok := registered.(*fs.ShellTool)
		if !ok {
			return toolresult.ToolResult{}, fmt.Errorf("memory %s is unavailable: %w", name, errInvalidProposal)
		}
		impl = shell
	default:
		return toolresult.ToolResult{}, errInvalidProposal
	}
	result := impl.Execute(fs.WithFileViews(ctx, f.views), args, f.dir)
	if !result.Success {
		return result, fmt.Errorf("memory %s: %s: %w", name, result.Error, errInvalidProposal)
	}
	return result, nil
}

func (f *updateFiles) read(ctx context.Context, note noteData) (string, error) {
	if err := f.open(); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(f.dir, note.File), []byte(note.Text), 0o600); err != nil {
		return "", err
	}
	if strings.ContainsRune(note.Text, 0) {
		return "", fmt.Errorf("memory read requires text: %w", errInvalidProposal)
	}
	if _, err := f.run(ctx, tool.ToolRead, map[string]any{"file_path": note.File}); err != nil {
		return "", err
	}
	// The shared tool records the staged file for Edit. The snapshot is already
	// bounded; return its exact bytes instead of a line-limited terminal view.
	return note.Text, nil
}

func (f *updateFiles) change(ctx context.Context, p *proposal) error {
	if err := f.open(); err != nil {
		return err
	}
	path := filepath.Join(f.dir, p.File)
	if p.Action == actionForget {
		if _, err := f.run(ctx, p.Shell, map[string]any{"command": forgetCommand(p.Shell, p.File)}); err != nil {
			return err
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return fmt.Errorf("shell did not remove the staged note: %w", errInvalidProposal)
		}
		return nil
	}
	name := tool.ToolEdit
	if p.Action == actionRemember {
		name = tool.ToolWrite
	}
	limit := maxNote
	if p.File == system.AutoMemoryIndexName {
		limit = maxIndex
	}
	for _, args := range p.Params {
		if name == tool.ToolEdit {
			if err := checkEditSize(path, args, limit); err != nil {
				return err
			}
		}
		if _, err := f.run(ctx, name, args); err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() > int64(limit) {
			return fmt.Errorf("memory note would exceed size limit: %w", errInvalidProposal)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	p.Text = string(data)
	if !core.ValidMultiline(p.Text, limit) || (limit == maxNote && strings.TrimSpace(p.Text) == "") {
		return errInvalidProposal
	}
	if p.Action == actionRemember {
		frontmatter, body, err := markdown.SplitFrontmatter(p.Text)
		if err != nil || !noteText(body) {
			return errInvalidProposal
		}
		meta, err := parseNoteMetadata(frontmatter)
		if err != nil || !validNoteMetadata(meta) {
			return errInvalidProposal
		}
		p.Name, p.Description, p.Type = meta.Name, meta.Description, meta.Type
	}
	return nil
}

// Bound expansion before the shared Edit tool builds the replacement and diff.
func checkEditSize(path string, args map[string]any, limit int) error {
	replacement, _ := args["new_string"].(string)
	if len(replacement) > limit {
		return fmt.Errorf("memory note would exceed size limit: %w", errInvalidProposal)
	}
	all, _ := args["replace_all"].(bool)
	old, _ := args["old_string"].(string)
	if !all || old == "" || len(replacement) <= len(old) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	old = strings.ReplaceAll(old, "\r\n", "\n")
	if max(1, strings.Count(text, old))*len(replacement) > 2*limit {
		return fmt.Errorf("memory note would exceed size limit: %w", errInvalidProposal)
	}
	return nil
}
