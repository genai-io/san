package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"

	"github.com/genai-io/sdk-go/pkg/ai"
)

type projectionData struct {
	SessionID            string
	Events               []Event
	Identity             SessionCreatePayload
	Messages             []ai.Message
	ActiveIDs            []string
	MessageSeq           map[string]uint64   // message ID -> seq of the event that appended it
	ToolCalls            map[string]toolCall // by tool call ID
	Config               EventConfig
	State                EventState
	Instructions         map[string]InstructionSection
	Tools                map[string]ai.Schema
	Skills               map[string]string
	UnfinishedInferences map[uint64]bool // inference.start seq, until its inference.finish
}

// toolCall follows one native tool call from the message that carries it,
// through the permission gate, to its tool.finish.
type toolCall struct {
	Seq      uint64 // event whose message carries the call
	Decision string // permission.decide result; empty until decided
	Finish   uint64 // seq of tool.finish; zero until finished
}

// Projection is an immutable validated prefix. Accessors return independent
// copies; applying a batch never modifies the input, including on failure.
// Audit facts (including unfinished operations) remain in Events, not live handles.
type Projection struct {
	data         projectionData
	messageIndex map[string]int
}

func (p *Projection) SessionID() string              { return p.data.SessionID }
func (p *Projection) Events() []Event                { return eventCopy(p.data.Events) }
func (p *Projection) Messages() []ai.Message         { return eventCopy(p.data.Messages) }
func (p *Projection) Identity() SessionCreatePayload { return eventCopy(p.data.Identity) }
func (p *Projection) Config() EventConfig            { return eventCopy(p.data.Config) }
func (p *Projection) State() EventState              { return eventCopy(p.data.State) }
func (p *Projection) Skills() map[string]string      { return maps.Clone(p.data.Skills) }

// ActiveMessages is the context sent to the model.
func (p *Projection) ActiveMessages() []ai.Message {
	out := make([]ai.Message, 0, len(p.data.ActiveIDs))
	for _, id := range p.data.ActiveIDs {
		out = append(out, p.message(id))
	}
	return eventCopy(out)
}

type NamedInstruction struct {
	Name string `json:"name"`
	InstructionSection
}

func (p *Projection) Instructions() []NamedInstruction {
	out := make([]NamedInstruction, 0, len(p.data.Instructions))
	for k, v := range p.data.Instructions {
		out = append(out, NamedInstruction{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (p *Projection) Tools() []ai.Schema {
	out := make([]ai.Schema, 0, len(p.data.Tools))
	for _, v := range p.data.Tools {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return eventCopy(out)
}

// CatalogueDigests includes known empty catalogues. Digests are SHA-256 of the
// deterministic, name-sorted native JSON definitions, not runtime installation.
func (p *Projection) CatalogueDigests() (instructions, tools string) {
	return eventDigest(p.Instructions()), eventDigest(p.Tools())
}
func eventDigest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Unfinished lists what a resume must report and never retry or complete:
// inference.start seqs without a finish, tool calls that never passed the gate
// (never ran), and allowed calls without tool.finish (unknown outcome).
type Unfinished struct {
	Inferences []uint64
	NeverRan   []string
	Unknown    []string
}

func (p *Projection) Unfinished() Unfinished {
	var u Unfinished
	for seq := range p.data.UnfinishedInferences {
		u.Inferences = append(u.Inferences, seq)
	}
	for id, c := range p.data.ToolCalls {
		switch {
		case c.Decision == "":
			u.NeverRan = append(u.NeverRan, id)
		case c.Decision == "allow" && c.Finish == 0:
			u.Unknown = append(u.Unknown, id)
		}
	}
	slices.Sort(u.Inferences)
	sort.Strings(u.NeverRan)
	sort.Strings(u.Unknown)
	return u
}

// eventCopy is only used on data already accepted through the native JSON codec.
func eventCopy[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out T
	if err = strictEventJSON(b, &out); err != nil {
		panic(err)
	}
	return out
}
func (p *Projection) event(seq uint64) *Event {
	if seq >= 1 && seq <= uint64(len(p.data.Events)) {
		return &p.data.Events[seq-1]
	}
	return nil
}
func (p *Projection) message(id string) ai.Message {
	if i, ok := p.messageIndex[id]; ok {
		return p.data.Messages[i]
	}
	return ai.Message{}
}

// ReplayEventBatches replays committed batches of one session. No IO or runtime
// callbacks occur.
func ReplayEventBatches(sessionID string, batches [][]Event, validation EventValidationContext) (*Projection, error) {
	var p *Projection
	var err error
	for _, batch := range batches {
		p, err = applyReplayEventBatch(p, sessionID, batch, validation)
		if err != nil {
			return nil, err
		}
	}
	if p == nil {
		return nil, fmt.Errorf("missing session.create")
	}
	return p, nil
}

// ApplyEventBatch validates all events before returning a replacement projection.
// It does not commit to storage. The durable owner must publish the replacement
// only after its atomic persistence boundary.
func ApplyEventBatch(base *Projection, sessionID string, events []Event, validation EventValidationContext) (*Projection, error) {
	var p *Projection
	if base != nil {
		// Accepted payloads and their nested native values are immutable. Only
		// containers modified by apply need copying; patches replace pointers,
		// never mutate their targets. Index maps belong to each branch too.
		copy := *base
		p = &copy
		p.data.Events = slices.Clone(base.data.Events)
		p.data.Messages = slices.Clone(base.data.Messages)
		p.data.ActiveIDs = slices.Clone(base.data.ActiveIDs)
		p.data.MessageSeq = maps.Clone(base.data.MessageSeq)
		p.data.ToolCalls = maps.Clone(base.data.ToolCalls)
		p.data.Instructions = maps.Clone(base.data.Instructions)
		p.data.Tools = maps.Clone(base.data.Tools)
		p.data.Skills = maps.Clone(base.data.Skills)
		p.data.UnfinishedInferences = maps.Clone(base.data.UnfinishedInferences)
		p.messageIndex = maps.Clone(base.messageIndex)
	}
	return applyReplayEventBatch(p, sessionID, events, validation)
}

// applyReplayEventBatch mutates an exclusively owned, unpublished builder.
// The caller must discard the whole builder on error, not publish a partial
// batch. A nil builder starts a new prefix. Never pass a published projection.
func applyReplayEventBatch(p *Projection, sessionID string, events []Event, validation EventValidationContext) (*Projection, error) {
	if p == nil {
		p = &Projection{
			data: projectionData{
				SessionID: sessionID, MessageSeq: map[string]uint64{}, ToolCalls: map[string]toolCall{},
				Instructions: map[string]InstructionSection{}, Tools: map[string]ai.Schema{}, Skills: map[string]string{},
				UnfinishedInferences: map[uint64]bool{},
			},
			messageIndex: map[string]int{},
		}
	}
	if p.data.SessionID != sessionID {
		return nil, fmt.Errorf("session mismatch")
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("empty event batch")
	}
	for _, incoming := range events {
		if incoming.Payload == nil || (reflect.ValueOf(incoming.Payload).Kind() == reflect.Pointer && reflect.ValueOf(incoming.Payload).IsNil()) {
			return nil, fmt.Errorf("nil payload")
		}
		if incoming.Type != incoming.Payload.eventType() {
			return nil, fmt.Errorf("payload/type mismatch")
		}
		b, err := json.Marshal(incoming)
		if err != nil {
			return nil, err
		}
		var e Event
		if err = json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		if err = p.apply(e, validation); err != nil {
			return nil, fmt.Errorf("event %d (%s): %w", e.Seq, e.Type, err)
		}
		p.data.Events = append(p.data.Events, e)
	}
	return p, nil
}
func patchValue[T any](dst **T, field PatchField[T]) {
	if field.Present {
		*dst = field.Value
	}
}
func applyConfigPatch(c *EventConfig, p ConfigPatch) {
	patchValue(&c.Provider, p.Provider)
	patchValue(&c.Model, p.Model)
	patchValue(&c.GenerationOptions, p.GenerationOptions)
	patchValue(&c.Mode, p.Mode)
	patchValue(&c.AutopilotPolicy, p.AutopilotPolicy)
}
func applyStatePatch(s *EventState, p StatePatch) {
	patchValue(&s.Title, p.Title)
	patchValue(&s.Todos, p.Todos)
	patchValue(&s.AutopilotProgress, p.AutopilotProgress)
	patchValue(&s.ExecutionCWD, p.ExecutionCWD)
	patchValue(&s.GroupHint, p.GroupHint)
}
