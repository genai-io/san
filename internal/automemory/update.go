// Package automemory proposes and applies bounded changes to repository Auto Memory.
package automemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/aside"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/markdown"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"gopkg.in/yaml.v3"
)

const (
	actionNone     Action = "none"
	actionRead     Action = "read"
	actionRemember Action = "remember"
	actionUpdate   Action = "update"
	actionForget   Action = "forget"
)

// noteTypes defines the classification contract for both validation and Propose.
// These labels never change where a note is stored.
var noteTypes = map[string]string{
	"user":      "Who the user is or what they generally prefer: role, expertise, or standing personal preference. A correction or an approach they confirmed in this repository is feedback instead.",
	"feedback":  "How the agent should work next time in this repository: a user correction or an approach they explicitly confirmed. Include why and when to apply it if known.",
	"project":   "What matters about ongoing work in this repository: meaningful milestones, deadlines, decisions, or constraints not recoverable from code, docs, or git. Include why and when it applies if known. Skip transient task status.",
	"reference": "Where to find a source of truth not already discoverable in the repository, such as an external dashboard or tracker. Save the pointer and its purpose, not a copy of its contents.",
}

// Updater proposes a bounded memory change without running shell commands.
type Updater struct {
	Provider    llm.Provider
	Model       string
	Recording   *session.Session
	IndexLimits setting.MemoryPreviewLimits
}

type proposal struct {
	Action      Action   `json:"action"`
	Files       []string `json:"files"`
	Query       string   `json:"query"`
	Line        int      `json:"line"`
	File        string   `json:"file"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	Body        string   `json:"body"`
	Edits       []noteEdit
}

type noteEdit struct {
	Old string `json:"old_string"`
	New string `json:"new_string"`
	All bool   `json:"replace_all"`
}

type noteData struct {
	File string
	Text string
}

// Change holds a validated proposal and its open recording. Its owner must
// apply or discard it after checking that the request is still current.
type Change struct {
	recording *session.Completion
	apply     func(context.Context) (string, error)
}

func (c *Change) Discard() error {
	if c == nil || c.apply == nil {
		return nil
	}
	c.apply = nil
	return c.recording.Close()
}

func (c *Change) Apply(ctx context.Context) (notice string, err error) {
	if c == nil || c.apply == nil {
		return "", nil
	}
	apply := c.apply
	c.apply = nil
	if err = ctx.Err(); err == nil {
		notice, err = apply(ctx)
	}
	return notice, aside.FinishChange(ctx, c.recording, notice, err)
}

var errInvalidProposal = errors.New("invalid memory proposal")

func validateProposal(p proposal) error {
	invalid := errInvalidProposal
	metadata := p.Name != "" || p.Description != "" || p.Type != "" || p.Body != ""
	patch := len(p.Edits) != 0
	if p.Action.IsIndexLookup() {
		if p.File != "" || p.Files != nil || metadata || patch {
			return invalid
		}
		return nil
	}
	if p.Query != "" || p.Line != 0 {
		return invalid
	}
	if p.Action == actionRead {
		if p.File != "" || metadata || patch {
			return invalid
		}
		return nil
	}
	if p.Files != nil {
		return invalid
	}
	if p.Action == actionNone && p.File == "" && !metadata && !patch {
		return nil
	}
	if !validName(p.File) {
		return invalid
	}
	switch p.Action {
	case actionRemember:
		if patch || !shortLabel(p.Name, 80) || !shortLabel(p.Description, 180) || !noteText(p.Body) {
			return invalid
		}
		if _, ok := noteTypes[p.Type]; !ok {
			return invalid
		}
	case actionUpdate:
		if metadata || !patch {
			return invalid
		}
		for _, edit := range p.Edits {
			if !noteText(edit.Old) || !noteText(edit.New) || edit.Old == edit.New {
				return invalid
			}
		}
	case actionForget:
		if metadata || patch {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}

func shortLabel(s string, maxBytes int) bool {
	return core.ValidLabel(s, maxBytes) && !strings.ContainsAny(s, `[]\<>`) && !mentionsNote(s)
}

func noteText(s string) bool {
	return strings.TrimSpace(s) != "" && core.ValidMultiline(s, maxNote)
}

func renderNote(p proposal) string {
	// JSON strings are valid YAML scalars, including quotes and punctuation.
	quote := func(s string) string { data, _ := json.Marshal(s); return string(data) }
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n\n%s\n", quote(p.Name), quote(p.Description), p.Type, strings.TrimSpace(p.Body))
}

type noteMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
}

func parseNoteMetadata(frontmatter string) (noteMetadata, error) {
	var meta noteMetadata
	d := yaml.NewDecoder(strings.NewReader(frontmatter))
	d.KnownFields(true)
	err := d.Decode(&meta)
	return meta, err
}

// patchNote validates the entire edit batch before the caller writes either
// file. Unchanged frontmatter is left alone, including on legacy notes.
func patchNote(text string, edits []noteEdit) (string, *noteMetadata, error) {
	frontmatter, _, err := markdown.SplitFrontmatter(text)
	if err != nil {
		return "", nil, fmt.Errorf("read memory frontmatter: %w", err)
	}
	for _, edit := range edits {
		i := strings.Index(text, edit.Old)
		if i < 0 {
			return "", nil, errors.New("memory edit text was not found in the note")
		}
		count := 1
		if edit.All {
			count = strings.Count(text, edit.Old)
		} else if strings.Contains(text[i+1:], edit.Old) {
			return "", nil, errors.New("memory edit must match a unique excerpt or use replace_all")
		}
		if len(text)+count*(len(edit.New)-len(edit.Old)) > maxNote {
			return "", nil, errors.New("memory note would exceed size limit")
		}
		text = strings.Replace(text, edit.Old, edit.New, count)
	}
	updated, body, err := markdown.SplitFrontmatter(text)
	if err != nil || !noteText(body) {
		return "", nil, errors.New("memory edit must leave valid frontmatter and a nonempty body")
	}
	if frontmatter == updated {
		return text, nil, nil
	}
	before, oldErr := parseNoteMetadata(frontmatter)
	after, newErr := parseNoteMetadata(updated)
	_, knownType := noteTypes[after.Type]
	if oldErr != nil || newErr != nil || !knownType || before.Type != after.Type ||
		!shortLabel(after.Name, 80) || !shortLabel(after.Description, 180) {
		return "", nil, errors.New("memory edit must preserve the note type and leave a valid name and description")
	}
	if before == after {
		return text, nil, nil
	}
	return text, &after, nil
}
