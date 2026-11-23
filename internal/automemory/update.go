// Package automemory proposes and applies bounded changes to repository Auto Memory.
package automemory

import (
	"context"
	"errors"
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

// Updater proposes a bounded change to the current project's memory.
type Updater struct {
	Provider    llm.Provider
	Model       string
	Recording   *session.Session
	IndexLimits setting.MemoryPreviewLimits
}

type proposal struct {
	Action      Action
	Files       []string
	Query       string
	Line        int
	File        string
	Name        string
	Description string
	Type        string
	Text        string
	Params      []map[string]any
	Shell       string
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

func shortLabel(s string, maxBytes int) bool {
	return core.ValidLabel(s, maxBytes) && !strings.ContainsAny(s, `[]\<>`) && !mentionsNote(s)
}

func noteText(s string) bool {
	return strings.TrimSpace(s) != "" && core.ValidMultiline(s, maxNote)
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

func validNoteMetadata(meta noteMetadata) bool {
	_, known := noteTypes[meta.Type]
	return known && shortLabel(meta.Name, 80) && shortLabel(meta.Description, 180)
}

// revisedMetadata leaves unchanged frontmatter alone, including on legacy notes.
func revisedMetadata(original, text string) (*noteMetadata, error) {
	frontmatter, _, err := markdown.SplitFrontmatter(original)
	if err != nil {
		return nil, err
	}
	updated, body, err := markdown.SplitFrontmatter(text)
	if err != nil || !noteText(body) {
		return nil, errors.New("memory edit must leave valid frontmatter and a nonempty body")
	}
	if frontmatter == updated {
		return nil, nil
	}
	before, oldErr := parseNoteMetadata(frontmatter)
	after, newErr := parseNoteMetadata(updated)
	if oldErr != nil || newErr != nil || before.Type != after.Type || !validNoteMetadata(after) {
		return nil, errors.New("memory edit must preserve the note type and leave a valid name and description")
	}
	if before == after {
		return nil, nil
	}
	return &after, nil
}
