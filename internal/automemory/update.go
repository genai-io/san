// Package automemory proposes and applies bounded changes to repository Auto Memory.
package automemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/genai-io/san/internal/aside"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/markdown"
	"github.com/genai-io/san/internal/session"
)

type memoryAction string

const (
	actionNone     memoryAction = "none"
	actionRemember memoryAction = "remember"
	actionUpdate   memoryAction = "update"
	actionForget   memoryAction = "forget"
)

// noteTypes defines the classification contract for both validation and Propose.
// These labels never change where a note is stored.
var noteTypes = map[string]string{
	"user":      "Who the user is or what they generally prefer: role, expertise, or standing personal preference. A correction or an approach they confirmed in this repository is feedback instead.",
	"feedback":  "How the agent should work next time in this repository: a user correction or an approach they explicitly confirmed. Include why and when to apply it if known.",
	"project":   "What matters about ongoing work in this repository: meaningful milestones, deadlines, decisions, or constraints not recoverable from code, docs, or git. Include why and when it applies if known. Skip transient task status.",
	"reference": "Where to find a source of truth not already discoverable in the repository, such as an external dashboard or tracker. Save the pointer and its purpose, not a copy of its contents.",
}

// Updater proposes a memory change with a tool-free model call.
type Updater struct {
	Provider  llm.Provider
	Model     string
	Recording *session.Session
}

type proposal struct {
	Action      memoryAction `json:"action"`
	File        string       `json:"file"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Type        string       `json:"type"`
	Body        string       `json:"body"`
	Old         string       `json:"old"`
	Replacement string       `json:"replacement"`
}

type noteEvidence struct {
	File string `json:"file"`
	Text string `json:"text"`
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

func decodeProposal(text string) (proposal, error) {
	var p proposal
	if len(text) > 16*1024 || !utf8.ValidString(text) || core.DecodeStrictJSON(strings.NewReader(text), &p) != nil {
		return p, errInvalidProposal
	}
	return p, nil
}

func validateProposal(p proposal) error {
	invalid := errInvalidProposal
	metadata := p.Name != "" || p.Description != "" || p.Type != "" || p.Body != ""
	patch := p.Old != "" || p.Replacement != ""
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
		if metadata || !noteText(p.Old) || !noteText(p.Replacement) {
			return invalid
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
	return aside.ValidLabel(s, maxBytes) && !strings.ContainsAny(s, `[]\<>`)
}

func noteText(s string) bool {
	return strings.TrimSpace(s) != "" && aside.ValidMultiline(s, maxNote)
}

func renderNote(p proposal) string {
	// JSON strings are valid YAML scalars, including quotes and punctuation.
	quote := func(s string) string { data, _ := json.Marshal(s); return string(data) }
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n\n%s\n", quote(p.Name), quote(p.Description), p.Type, strings.TrimSpace(p.Body))
}

func patchNote(text string, p proposal) (string, error) {
	_, body, err := markdown.SplitFrontmatter(text)
	if err != nil {
		return "", errors.New("unterminated memory frontmatter")
	}
	i := strings.Index(body, p.Old)
	if i < 0 || strings.Contains(body[i+1:], p.Old) {
		return "", errors.New("memory update must match one unique excerpt in the note body")
	}
	return text[:len(text)-len(body)] + body[:i] + p.Replacement + body[i+len(p.Old):], nil
}
