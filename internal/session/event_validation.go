package session

import (
	"encoding/hex"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/ai"
)

const (
	maxReason  = 4096
	maxDetails = 1 << 20
)

// ForeignEventEvidence is acceptance-time provenance supplied by a trusted
// adapter, not permission to execute anything. Keep this evidence alongside the
// committed prefix for replay: replay must not consult a surviving foreign session.
type ForeignEventEvidence struct {
	Type       EventType
	MessageIDs []string
}

// foreignKey addresses an event of another session in EventValidationContext.
func foreignKey(session string, seq uint64) string {
	return session + "#" + strconv.FormatUint(seq, 10)
}
func parseForeignKey(key string) (string, uint64, bool) {
	i := strings.LastIndexByte(key, '#')
	if i < 1 {
		return "", 0, false
	}
	seq, err := strconv.ParseUint(key[i+1:], 10, 64)
	return key[:i], seq, err == nil && seq > 0
}

// EventValidationContext contains only caller-validated facts. The caller owns
// project/trust/asset validation and persistence of this acceptance evidence.
// ForeignEvents is keyed by foreignKey. ParentAncestors must include the parent
// itself; ForkPrefixes binds source session IDs to verified prefix lengths.
// This projector neither obtains evidence nor manufactures missing events.
type EventValidationContext struct {
	ForeignEvents   map[string]ForeignEventEvidence
	ParentAncestors map[string][]string
	ForkPrefixes    map[string]uint64
}

// mergeValidation retains acceptance data, never permission or live foreign IO.
// Each merge sees only the committed prefix and the current batch, without
// modifying either input.
func mergeValidation(prior, current EventValidationContext) (EventValidationContext, error) {
	out := EventValidationContext{ForeignEvents: maps.Clone(prior.ForeignEvents), ParentAncestors: maps.Clone(prior.ParentAncestors), ForkPrefixes: maps.Clone(prior.ForkPrefixes)}
	return mergeReplayValidation(out, current)
}

// mergeReplayValidation requires exclusively owned output, discarded on any error.
func mergeReplayValidation(out, current EventValidationContext) (EventValidationContext, error) {
	if out.ForeignEvents == nil {
		out.ForeignEvents = map[string]ForeignEventEvidence{}
	}
	if out.ParentAncestors == nil {
		out.ParentAncestors = map[string][]string{}
	}
	if out.ForkPrefixes == nil {
		out.ForkPrefixes = map[string]uint64{}
	}
	validID := func(id string) bool { return transcript.ValidateStorageID(id) == nil }
	uniqueIDs := func(ids []string) bool {
		seen := map[string]bool{}
		for _, id := range ids {
			if !validID(id) || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	for key, fact := range current.ForeignEvents {
		session, _, ok := parseForeignKey(key)
		if !ok || !validID(session) || !knownEventType(fact.Type) || !uniqueIDs(fact.MessageIDs) {
			return out, fmt.Errorf("malformed foreign evidence %q", key)
		}
		if len(fact.MessageIDs) > 0 && fact.Type != EventMessageAppend {
			return out, fmt.Errorf("non-message evidence has messages")
		}
		if old, ok := out.ForeignEvents[key]; ok && !reflect.DeepEqual(old, fact) {
			return out, fmt.Errorf("contradictory foreign evidence %q", key)
		}
		fact.MessageIDs = slices.Clone(fact.MessageIDs)
		out.ForeignEvents[key] = fact
	}
	for id, ancestors := range current.ParentAncestors {
		if !validID(id) || len(ancestors) == 0 || !uniqueIDs(ancestors) || !slices.Contains(ancestors, id) {
			return out, fmt.Errorf("malformed ancestor evidence %q", id)
		}
		if old, ok := out.ParentAncestors[id]; ok && !reflect.DeepEqual(old, ancestors) {
			return out, fmt.Errorf("contradictory ancestor evidence %q", id)
		}
		out.ParentAncestors[id] = slices.Clone(ancestors)
	}
	for id, seq := range current.ForkPrefixes {
		if !validID(id) || seq == 0 {
			return out, fmt.Errorf("malformed fork evidence %q", id)
		}
		if old, ok := out.ForkPrefixes[id]; ok && old != seq {
			return out, fmt.Errorf("contradictory fork evidence %q", id)
		}
		out.ForkPrefixes[id] = seq
	}
	return out, nil
}

func invalidEvent(format string, args ...any) error { return fmt.Errorf(format, args...) }

// source resolves an event's provenance link to the type and message IDs it names.
func (p *Projection) source(e Event, validation EventValidationContext) (EventType, []string, bool, error) {
	if e.Source == nil {
		return "", nil, false, nil
	}
	if e.Source.Seq == 0 {
		return "", nil, false, invalidEvent("source needs a seq")
	}
	if e.Source.Session == "" {
		src := p.event(e.Source.Seq)
		if src == nil {
			return "", nil, false, invalidEvent("unresolved source")
		}
		return src.Type, p.messageIDsOf(src), true, nil
	}
	if e.Source.Session == p.data.SessionID {
		return "", nil, false, invalidEvent("source names its own session explicitly")
	}
	f, ok := validation.ForeignEvents[foreignKey(e.Source.Session, e.Source.Seq)]
	if !ok {
		return "", nil, false, invalidEvent("unresolved foreign source")
	}
	return f.Type, f.MessageIDs, true, nil
}
func (p *Projection) messageIDsOf(e *Event) []string {
	switch v := e.Payload.(type) {
	case *MessageAppendPayload:
		ids := make([]string, len(v.Messages))
		for i, m := range v.Messages {
			ids[i] = m.ID
		}
		return ids
	case *ContextCompactPayload:
		return []string{v.Summary.ID}
	}
	return nil
}

// closeInference ties an inference.finish to its unfinished local start.
func (p *Projection) closeInference(e Event) (*InferenceStartPayload, error) {
	if e.Source == nil || e.Source.Session != "" {
		return nil, invalidEvent("finish needs a local source")
	}
	start := p.event(e.Source.Seq)
	if start == nil || start.Type != EventInferenceStart {
		return nil, invalidEvent("finish source must be inference.start")
	}
	if _, done := p.data.Terminals[start.Seq]; done {
		return nil, invalidEvent("duplicate terminal result")
	}
	p.data.Terminals[start.Seq] = e.Seq
	return start.Payload.(*InferenceStartPayload), nil
}

func (p *Projection) apply(e Event, validation EventValidationContext) error {
	if e.Seq != uint64(len(p.data.Events))+1 {
		return invalidEvent("noncontiguous sequence")
	}
	if e.SchemaVersion != 1 {
		return invalidEvent("unsupported schema version %d", e.SchemaVersion)
	}
	if e.Time < 0 {
		return invalidEvent("negative time")
	}
	if (e.Seq == 1) != (e.Type == EventSessionCreate) {
		return invalidEvent("session.create must be exactly the first event")
	}
	srcType, _, srcLocal, err := p.source(e, validation)
	if err != nil {
		return err
	}
	switch v := e.Payload.(type) {
	case *SessionCreatePayload:
		if v.ProjectID == "" || v.AgentID == "" || !filepath.IsAbs(v.Workspace) || !filepath.IsAbs(v.InitialCWD) {
			return invalidEvent("invalid creation identity")
		}
		if v.Parent != nil {
			ancestors, ok := validation.ParentAncestors[v.Parent.Session]
			if !ok || !slices.Contains(ancestors, v.Parent.Session) || slices.Contains(ancestors, p.data.SessionID) {
				return invalidEvent("unvalidated or cyclic parent")
			}
		}
		if f := v.Fork; f != nil && (f.Session == p.data.SessionID || f.Seq == 0 || validation.ForkPrefixes[f.Session] < f.Seq) {
			return invalidEvent("unvalidated fork prefix")
		}
		if v.Config != nil {
			if err := validateEventConfig(*v.Config); err != nil {
				return err
			}
			p.data.Config = *v.Config
		}
		p.data.Identity = *v
		cwd := v.InitialCWD
		p.data.State.ExecutionCWD = &cwd
	case *SessionUpdatePayload:
		if v.Config == nil && v.State == nil {
			return invalidEvent("empty session update")
		}
		beforeConfig, beforeState := eventCopy(p.data.Config), eventCopy(p.data.State)
		if v.Config != nil {
			applyConfigPatch(&p.data.Config, *v.Config)
		}
		if v.State != nil {
			applyStatePatch(&p.data.State, *v.State)
		}
		if reflect.DeepEqual(beforeConfig, p.data.Config) && reflect.DeepEqual(beforeState, p.data.State) {
			return invalidEvent("unchanged session update")
		}
		if err := validateEventConfig(p.data.Config); err != nil {
			return err
		}
		return validateEventState(p.data.State)
	case *MessageAppendPayload:
		if len(v.Messages) == 0 {
			return invalidEvent("empty message batch")
		}
		if e.Source != nil && srcType != EventInferenceStart && srcType != EventMessageAppend && srcType != EventContextCompact {
			return invalidEvent("invalid message source family")
		}
		for _, m := range v.Messages {
			if m.Role == ai.RoleAssistant && e.Source == nil {
				return invalidEvent("assistant message needs explicit provenance")
			}
			if err := p.addMessage(m, e.Seq); err != nil {
				return err
			}
			p.data.ActiveIDs = append(p.data.ActiveIDs, m.ID)
		}
	case *ContextCompactPayload:
		if v.Through == 0 || v.Through >= e.Seq {
			return invalidEvent("compaction through must name a committed event")
		}
		if e.Source != nil {
			start, ok := p.event(e.Source.Seq).Payload.(*InferenceStartPayload)
			if !srcLocal || !ok || start.Purpose != PurposeCompaction {
				return invalidEvent("summary source must be a local compaction inference.start")
			}
		}
		if err := p.addMessage(v.Summary, e.Seq); err != nil {
			return err
		}
		active := []string{v.Summary.ID}
		for _, id := range p.data.ActiveIDs {
			if p.data.MessageSeq[id] > v.Through {
				active = append(active, id)
			}
		}
		if err := p.validateToolPairs(active); err != nil {
			return err
		}
		p.data.ActiveIDs = active
	case *InferenceStartPayload:
		return p.validateInferenceStart(*v)
	case *InferenceFinishPayload:
		if _, err := p.closeInference(e); err != nil {
			return err
		}
		return validateTerminal(v.Outcome, v.Error, v.Reason, v.DurationMS)
	case *PermissionDecidePayload:
		if _, ok := p.data.CallSeq[v.CallID]; !ok {
			return invalidEvent("decision for unknown tool call")
		}
		if p.data.Decisions[v.CallID] != "" {
			return invalidEvent("tool call already decided")
		}
		if v.Decision != "allow" && v.Decision != "deny" && v.Decision != "cancelled" {
			return invalidEvent("invalid permission decision")
		}
		switch v.By {
		case ByConfig, ByMode, ByGrant, ByUser, ByHook, ByJudge:
		default:
			return invalidEvent("invalid decider")
		}
		if v.Decision == "cancelled" && v.Reason == "" || len(v.Reason) > maxReason || len(v.Scope) > 1024 {
			return invalidEvent("invalid decision reason or scope")
		}
		if v.Asked != nil && (len(v.Asked.Detail) > maxDetails || v.Asked.At < 0) {
			return invalidEvent("invalid asked record")
		}
		p.data.Decisions[v.CallID] = v.Decision
	case *ToolFinishPayload:
		if _, ok := p.data.CallSeq[v.CallID]; !ok {
			return invalidEvent("finish for unknown tool call")
		}
		if p.data.Decisions[v.CallID] != "allow" {
			return invalidEvent("tool.finish needs an allow decision")
		}
		if p.data.ToolFinished[v.CallID] {
			return invalidEvent("duplicate tool.finish")
		}
		if len(v.Details) > maxDetails || (v.ChildSession != "" && transcript.ValidateStorageID(v.ChildSession) != nil) || v.ChildSession == p.data.SessionID {
			return invalidEvent("invalid tool details or child session")
		}
		p.data.ToolFinished[v.CallID] = true
		return validateTerminal(v.Outcome, v.Error, v.Reason, v.DurationMS)
	case *InstructionsUpdatePayload:
		return updateCatalogue(p.data.Instructions, v.Set, v.Remove, v.Cause, func(name string, s InstructionSection) error {
			if s.Content == "" || s.Slot < 0 {
				return invalidEvent("invalid instruction section %q", name)
			}
			return nil
		})
	case *ToolsetUpdatePayload:
		return updateCatalogue(p.data.Tools, v.Set, v.Remove, v.Cause, func(name string, s ai.Schema) error {
			if s.Name != name {
				return invalidEvent("tool schema name must equal its key %q", name)
			}
			return nil
		})
	case *SkillsUpdatePayload:
		return updateCatalogue(p.data.Skills, v.Set, v.Remove, v.Cause, func(name, state string) error {
			if state != SkillDisabled && state != SkillEnabled && state != SkillActive {
				return invalidEvent("invalid skill state %q", name)
			}
			return nil
		})
	case *HookFinishPayload:
		if v.Hook == "" || v.Trigger == "" || (v.DurationMS != nil && *v.DurationMS < 0) || len(v.Reason) > maxReason {
			return invalidEvent("invalid hook finish")
		}
		if v.Outcome != HookRan && v.Outcome != HookBlocked && v.Outcome != HookError {
			return invalidEvent("invalid hook outcome")
		}
		if v.Outcome != HookRan && v.Reason == "" {
			return invalidEvent("blocked or failed hook needs reason")
		}
	case *AnnotationAddPayload:
		if v.Text == "" {
			return invalidEvent("empty annotation")
		}
		if v.AfterMessage != "" && p.message(v.AfterMessage).ID == "" {
			return invalidEvent("unknown anchor message")
		}
	default:
		return invalidEvent("unsupported payload")
	}
	return nil
}

func (p *Projection) validateInferenceStart(v InferenceStartPayload) error {
	switch v.Purpose {
	case PurposeTurn, PurposeBtw, PurposeRecap, PurposeSuggest, PurposeCompaction, PurposeJudge, PurposeHook, PurposeAutopilot, PurposeGroup:
	default:
		return invalidEvent("unknown inference purpose %q", v.Purpose)
	}
	if v.Purpose == PurposeTurn && v.TurnID == "" {
		return invalidEvent("missing turn ID")
	}
	if err := validateEventConfig(v.Request); err != nil {
		return err
	}
	if v.Request.Provider == nil || v.Request.Model == nil {
		return invalidEvent("actual request provider/model required")
	}
	in := v.Input
	if (in.Conversation == nil) == (in.Standalone == "") {
		return invalidEvent("input must be exactly one of conversation or standalone")
	}
	if in.Conversation != nil {
		instructions, tools := p.CatalogueDigests()
		if v.InstructionsDigest != instructions || v.ToolsetDigest != tools {
			return invalidEvent("conversation call must match projected catalogues; record it as standalone")
		}
		seen := map[string]bool{}
		for _, m := range in.Conversation.Append {
			if m.ID == "" || seen[m.ID] || (m.Role != ai.RoleUser && m.Role != ai.RoleAssistant) {
				return invalidEvent("invalid appended message")
			}
			seen[m.ID] = true
		}
		return nil
	}
	if b, err := hex.DecodeString(in.Standalone); err != nil || len(b) != 32 {
		return invalidEvent("standalone input must be a SHA-256 digest")
	}
	return nil
}

// updateCatalogue applies a set/remove change to a named catalogue. Each name
// appears once across set and remove, removals must exist, and a change that
// leaves the catalogue as it was is rejected.
func updateCatalogue[T any](cat map[string]T, set map[string]T, remove []string, cause string, check func(string, T) error) error {
	if cause == "" || len(set)+len(remove) == 0 {
		return invalidEvent("empty catalogue change or missing cause")
	}
	seen := map[string]bool{}
	for name, v := range set {
		if name == "" {
			return invalidEvent("empty catalogue name")
		}
		if err := check(name, v); err != nil {
			return err
		}
		seen[name] = true
	}
	for _, name := range remove {
		if name == "" || seen[name] {
			return invalidEvent("duplicate/overlapping/empty catalogue name")
		}
		if _, ok := cat[name]; !ok {
			return invalidEvent("unknown catalogue removal %q", name)
		}
		seen[name] = true
	}
	changed := false
	for name, v := range set {
		if old, ok := cat[name]; !ok || !reflect.DeepEqual(old, v) {
			changed = true
		}
		cat[name] = v
	}
	for _, name := range remove {
		delete(cat, name)
		changed = true
	}
	if !changed {
		return invalidEvent("unchanged catalogue")
	}
	return nil
}

func validateTerminal(outcome EventOutcome, err *EventError, reason string, duration *int64) error {
	if outcome != OutcomeSuccess && outcome != OutcomeFailure && outcome != OutcomeCancelled {
		return invalidEvent("invalid outcome")
	}
	if duration != nil && *duration < 0 || len(reason) > maxReason {
		return invalidEvent("invalid duration or unbounded reason")
	}
	if err != nil && (err.Message == "" || len(err.Message) > maxReason) {
		return invalidEvent("invalid structured error")
	}
	if outcome == OutcomeFailure && err == nil {
		return invalidEvent("failure needs structured error")
	}
	if outcome == OutcomeCancelled && reason == "" {
		return invalidEvent("cancellation needs reason")
	}
	if outcome == OutcomeSuccess && err != nil {
		return invalidEvent("successful finish has error")
	}
	return nil
}
func validateEventConfig(c EventConfig) error {
	if c.GenerationOptions != nil {
		g := c.GenerationOptions
		if g.Version != 1 || (g.MaxTokens != nil && *g.MaxTokens < 0) || (g.Temperature != nil && (math.IsNaN(*g.Temperature) || math.IsInf(*g.Temperature, 0) || *g.Temperature < 0)) {
			return invalidEvent("invalid generation options")
		}
	}
	if c.AutopilotPolicy != nil && (c.AutopilotPolicy.Version != 1 || c.AutopilotPolicy.MaxTurns < 0) {
		return invalidEvent("invalid autopilot policy")
	}
	return nil
}
func validateEventState(s EventState) error {
	if s.ExecutionCWD != nil && !filepath.IsAbs(*s.ExecutionCWD) {
		return invalidEvent("execution cwd must be absolute")
	}
	if s.GroupHint != nil && (s.GroupHint.GroupID == "" || s.GroupHint.MemberID == "") {
		return invalidEvent("incomplete group hint")
	}
	if s.AutopilotProgress != nil && s.AutopilotProgress.CompletedTurns < 0 {
		return invalidEvent("negative completed turns")
	}
	if s.Todos != nil {
		seen := map[string]bool{}
		for _, todo := range *s.Todos {
			if todo.ID == "" || seen[todo.ID] || todo.Text == "" || (todo.Status != "pending" && todo.Status != "in_progress" && todo.Status != "completed") {
				return invalidEvent("invalid todo")
			}
			seen[todo.ID] = true
		}
	}
	return nil
}

// addMessage registers a native message appended by the event at seq.
func (p *Projection) addMessage(m ai.Message, seq uint64) error {
	if m.ID == "" || p.message(m.ID).ID != "" {
		return invalidEvent("empty or duplicate native message ID")
	}
	if m.Role != ai.RoleUser && m.Role != ai.RoleAssistant {
		return invalidEvent("invalid native message role")
	}
	calls := map[string]bool{}
	results := map[string]bool{}
	for _, b := range m.Content {
		if b.Type == ai.BlockToolCall {
			if m.Role != ai.RoleAssistant || b.ToolCall == nil || b.ToolCall.ID == "" || b.ToolCall.Name == "" || calls[b.ToolCall.ID] {
				return invalidEvent("invalid or ambiguous native tool call")
			}
			if _, dup := p.data.CallSeq[b.ToolCall.ID]; dup {
				return invalidEvent("tool call ID reused in session")
			}
			calls[b.ToolCall.ID] = true
		}
		if b.Type == ai.BlockToolResult {
			if m.Role != ai.RoleUser || b.ToolResult == nil || b.ToolResult.ToolCallID == "" || results[b.ToolResult.ToolCallID] {
				return invalidEvent("invalid or duplicate native tool result")
			}
			results[b.ToolResult.ToolCallID] = true
		}
	}
	for id := range calls {
		p.data.CallSeq[id] = seq
	}
	p.messageIndex[m.ID] = len(p.data.Messages)
	p.data.Messages = append(p.data.Messages, m)
	p.data.MessageSeq[m.ID] = seq
	return nil
}
func (p *Projection) validateToolPairs(ids []string) error {
	calls := map[string]string{}
	done := map[string]bool{}
	for _, id := range ids {
		for _, b := range p.message(id).Content {
			if b.Type == ai.BlockToolCall {
				if b.ToolCall == nil || calls[b.ToolCall.ID] != "" {
					return invalidEvent("duplicate tool call in active context")
				}
				calls[b.ToolCall.ID] = b.ToolCall.Name
			}
			if b.Type == ai.BlockToolResult {
				if b.ToolResult == nil || calls[b.ToolResult.ToolCallID] == "" || done[b.ToolResult.ToolCallID] || (b.ToolResult.ToolName != "" && b.ToolResult.ToolName != calls[b.ToolResult.ToolCallID]) {
					return invalidEvent("unpaired tool result in active context")
				}
				done[b.ToolResult.ToolCallID] = true
			}
		}
	}
	for id := range calls {
		if !done[id] {
			return invalidEvent("unpaired tool call in compacted context")
		}
	}
	return nil
}
