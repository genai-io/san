package session

import (
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// ForeignEventEvidence is acceptance-time provenance supplied by a trusted
// adapter, not permission to execute anything. Keep this evidence alongside the
// committed prefix for replay: replay must not consult a surviving foreign log.
type ForeignEventEvidence struct {
	SessionID  string
	Type       EventType
	MessageIDs []string
}

// EventValidationContext contains only caller-validated facts. The caller owns
// global ID collision checks, project/trust/asset validation, and persistence of
// this acceptance evidence. ParentAncestors must include the parent itself;
// ForkPrefixes binds source session IDs to verified available prefix lengths.
// This projector neither obtains evidence nor manufactures missing starts.
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
// Mutating its evidence maps avoids copying all prior facts once per batch.
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
	for id, fact := range current.ForeignEvents {
		if !validID(id) || !validID(fact.SessionID) || !knownEventType(fact.Type) || !uniqueIDs(fact.MessageIDs) {
			return out, fmt.Errorf("malformed foreign evidence %q", id)
		}
		if len(fact.MessageIDs) > 0 && fact.Type != EventMessageAppend {
			return out, fmt.Errorf("non-message evidence has messages")
		}
		if old, ok := out.ForeignEvents[id]; ok && !reflect.DeepEqual(old, fact) {
			return out, fmt.Errorf("contradictory foreign evidence %q", id)
		}
		fact.MessageIDs = slices.Clone(fact.MessageIDs)
		out.ForeignEvents[id] = fact
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
func (p *Projection) apply(e Event, tail uint64, validation EventValidationContext) error {
	if e.ID == "" || e.SessionID == "" || e.Seq != uint64(len(p.data.Events))+1 {
		return invalidEvent("invalid identity or noncontiguous sequence")
	}
	if e.SchemaVersion != 1 {
		return invalidEvent("unsupported schema version %d", e.SchemaVersion)
	}
	if e.CreatedAt < 0 {
		return invalidEvent("negative created_at")
	}
	if len(p.data.Events) > 0 && e.SessionID != p.data.Events[0].SessionID {
		return invalidEvent("session mismatch")
	}
	if e.Seq == 1 && e.Type != EventSessionCreate {
		return invalidEvent("first event must create session")
	}
	if _, exists := validation.ForeignEvents[e.ID]; exists {
		return invalidEvent("event ID collides with foreign evidence")
	}
	if e.SourceID != "" {
		if p.event(e.SourceID) == nil {
			f, ok := validation.ForeignEvents[e.SourceID]
			if !ok || f.SessionID == "" || f.SessionID == e.SessionID || !knownEventType(f.Type) {
				return invalidEvent("unresolved source_id")
			}
		}
	}
	finish := e.Type == EventInferenceFinish || e.Type == EventToolFinish || e.Type == EventDelegationFinish || e.Type == EventHookFinish
	if finish {
		if e.Outcome != OutcomeSuccess && e.Outcome != OutcomeFailure && e.Outcome != OutcomeCancelled {
			return invalidEvent("invalid terminal outcome")
		}
	} else if e.Outcome != "" {
		return invalidEvent("unexpected outcome")
	}
	switch v := e.Payload.(type) {
	case *SessionCreatePayload:
		if e.Seq != 1 || v.ProjectID == "" || v.AgentID == "" || !filepath.IsAbs(v.Workspace) || !filepath.IsAbs(v.InitialCWD) {
			return invalidEvent("invalid creation identity")
		}
		if v.ParentSessionID != "" {
			ancestors, ok := validation.ParentAncestors[v.ParentSessionID]
			if !ok || !slices.Contains(ancestors, v.ParentSessionID) || slices.Contains(ancestors, e.SessionID) {
				return invalidEvent("unvalidated or cyclic parent")
			}
		}
		if (v.ForkSourceID == "") != (v.ForkSourceSeq == 0) {
			return invalidEvent("incomplete fork provenance")
		}
		if v.ForkSourceID != "" && (v.ForkSourceID == e.SessionID || validation.ForkPrefixes[v.ForkSourceID] < v.ForkSourceSeq) {
			return invalidEvent("unvalidated fork prefix")
		}
		if v.InitialConfig != nil {
			if err := validateEventConfig(*v.InitialConfig); err != nil {
				return err
			}
			p.data.Config = *v.InitialConfig
		}
		p.data.Identity = *v
		cwd := v.InitialCWD
		p.data.State.ExecutionCWD = &cwd
	case *MessageAppendPayload:
		if e.SourceID != "" {
			t := validation.ForeignEvents[e.SourceID].Type
			if source := p.event(e.SourceID); source != nil {
				t = source.Type
			}
			if t != EventInferenceStart && t != EventToolStart && t != EventDelegationStart && t != EventMessageAppend && t != EventCompactionApply {
				return invalidEvent("invalid message source family")
			}
		}
		if len(v.Messages) == 0 {
			return invalidEvent("empty message batch")
		}
		if len(v.SourceMessageIDs) > 0 {
			if e.SourceID == "" || len(v.SourceMessageIDs) != len(v.Messages) {
				return invalidEvent("unaligned source message IDs")
			}
			sourceIDs := p.sourceMessageIDs(e.SourceID, validation)
			for _, id := range v.SourceMessageIDs {
				if id == "" || !slices.Contains(sourceIDs, id) {
					return invalidEvent("unresolved source message ID")
				}
			}
		}
		for _, m := range v.Messages {
			if m.Role == ai.RoleAssistant && e.SourceID == "" {
				return invalidEvent("assistant message needs explicit provenance")
			}
			if err := p.addMessage(m); err != nil {
				return err
			}
			p.data.ActiveIDs = append(p.data.ActiveIDs, m.ID)
		}
	case *CompactionApplyPayload:
		if v.SourceSeq == 0 || v.SourceSeq != tail {
			return invalidEvent("stale compaction source tail")
		}
		if e.SourceID != "" {
			source := p.event(e.SourceID)
			if source == nil || source.Type != EventInferenceStart {
				return invalidEvent("summary source must be local inference.start")
			}
		}
		last := -1
		for _, id := range v.RetainedMessageIDs {
			// Search only the unconsumed suffix: validating a subsequence is
			// linear in the active history, not retained IDs times history.
			offset := slices.Index(p.data.ActiveIDs[last+1:], id)
			if offset < 0 {
				return invalidEvent("retained IDs must be unique active subsequence")
			}
			last += offset + 1
		}
		if err := p.addMessage(v.Summary); err != nil {
			return err
		}
		active := append([]string{v.Summary.ID}, v.RetainedMessageIDs...)
		if err := p.validateToolPairs(active); err != nil {
			return err
		}
		p.data.ActiveIDs = active
	case *ConfigUpdatePayload:
		before := eventCopy(p.data.Config)
		applyConfigPatch(&p.data.Config, v.Patch)
		if reflect.DeepEqual(before, p.data.Config) {
			return invalidEvent("empty or unchanged config patch")
		}
		if err := validateEventConfig(p.data.Config); err != nil {
			return err
		}
	case *StateUpdatePayload:
		before := eventCopy(p.data.State)
		applyStatePatch(&p.data.State, v.Patch)
		if reflect.DeepEqual(before, p.data.State) {
			return invalidEvent("empty or unchanged state patch")
		}
		if err := validateEventState(p.data.State); err != nil {
			return err
		}
	case *InferenceStartPayload:
		if v.TurnID == "" {
			return invalidEvent("missing turn ID")
		}
		ids := map[string]bool{}
		for _, id := range v.InputMessageIDs {
			if p.message(id).ID == "" || ids[id] {
				return invalidEvent("invalid inference input")
			}
			ids[id] = true
		}
		if err := validateEventConfig(v.RequestConfig); err != nil {
			return err
		}
		if v.RequestConfig.Provider == nil || v.RequestConfig.Model == nil {
			return invalidEvent("actual request provider/model required")
		}
		instructions, tools := p.CatalogueDigests()
		if v.InstructionDigest != instructions || v.ToolsetDigest != tools {
			return invalidEvent("unresolvable catalogue digest")
		}
	case *InferenceFinishPayload:
		if err := p.closeOperation(e, EventInferenceStart); err != nil {
			return err
		}
		return validateTerminal(e.Outcome, v.Error, v.Reason, v.DurationMS)
	case *ToolStartPayload:
		if v.CallID == "" || v.InputRef.CallID != v.CallID {
			return invalidEvent("tool call mismatch")
		}
		return p.validateInput(v.InputRef)
	case *ToolFinishPayload:
		if err := p.closeOperation(e, EventToolStart); err != nil {
			return err
		}
		start := p.event(e.SourceID).Payload.(*ToolStartPayload)
		if v.CallID != start.CallID {
			return invalidEvent("tool finish call mismatch")
		}
		if v.Details != nil && len(v.Details.DisplayText) > 4096 {
			return invalidEvent("unbounded tool details")
		}
		return validateTerminal(e.Outcome, v.Error, v.Reason, v.DurationMS)
	case *DelegationStartPayload:
		if v.ChildSessionID == "" || v.ChildSessionID == e.SessionID {
			return invalidEvent("invalid child session")
		}
		return p.validateInput(v.InputRef)
	case *DelegationFinishPayload:
		if err := p.closeOperation(e, EventDelegationStart); err != nil {
			return err
		}
		if v.ResultEventID != "" {
			result := p.event(v.ResultEventID)
			if result == nil || result.Type != EventMessageAppend {
				return invalidEvent("result must be locally accepted message event")
			}
		}
		return validateTerminal(e.Outcome, v.Error, v.Reason, nil)
	case *PermissionRequestPayload:
		operation := p.event(v.OperationRef)
		if operation == nil || (operation.Type != EventToolStart && operation.Type != EventInferenceStart && operation.Type != EventDelegationStart) || p.data.EndByStart[v.OperationRef] != "" {
			return invalidEvent("invalid protected operation")
		}
		return validateScope(v.Scope)
	case *PermissionDecidePayload:
		if err := p.closeOperation(e, EventPermissionRequest); err != nil {
			return err
		}
		if v.Decision != "allow" && v.Decision != "deny" && v.Decision != "cancelled" {
			return invalidEvent("invalid permission decision")
		}
		if v.Decision == "cancelled" && v.Reason == "" {
			return invalidEvent("cancellation needs reason")
		}
		if err := validateScope(v.Scope); err != nil {
			return err
		}
		request := p.event(e.SourceID).Payload.(*PermissionRequestPayload)
		for _, resource := range v.Scope.Resources {
			if !slices.Contains(request.Scope.Resources, resource) {
				return invalidEvent("permission scope expansion")
			}
		}
	case *HookFinishPayload:
		if v.HookName == "" || v.Trigger == "" || (v.DurationMS != nil && *v.DurationMS < 0) || len(v.Reason) > 4096 {
			return invalidEvent("invalid hook finish")
		}
		// Hook payload has no error member in the proposal; a bounded reason carries
		// its known failure/cancellation explanation, never a fabricated hook start.
		if e.Outcome != OutcomeSuccess && v.Reason == "" {
			return invalidEvent("hook failure/cancellation needs reason")
		}
		for _, ref := range v.EffectRefs {
			if p.event(ref) == nil {
				return invalidEvent("unresolved hook effect")
			}
		}
	case *InstructionUpdatePayload:
		names := make([]string, len(v.Upsert))
		for i, item := range v.Upsert {
			names[i] = item.Name
		}
		if err := validateNames(names, v.Remove); err != nil {
			return err
		}
		before := eventCopy(p.data.Instructions)
		for _, item := range v.Upsert {
			p.data.Instructions[item.Name] = item
		}
		for _, name := range v.Remove {
			if _, ok := p.data.Instructions[name]; !ok {
				return invalidEvent("unknown instruction removal")
			}
			delete(p.data.Instructions, name)
		}
		if reflect.DeepEqual(before, p.data.Instructions) {
			return invalidEvent("unchanged instructions")
		}
	case *ToolsetUpdatePayload:
		names := make([]string, len(v.Upsert))
		for i, item := range v.Upsert {
			names[i] = item.Name
		}
		if err := validateNames(names, v.Remove); err != nil {
			return err
		}
		before := eventCopy(p.data.Tools)
		for _, item := range v.Upsert {
			p.data.Tools[item.Name] = item
		}
		for _, name := range v.Remove {
			if _, ok := p.data.Tools[name]; !ok {
				return invalidEvent("unknown tool removal")
			}
			delete(p.data.Tools, name)
		}
		if reflect.DeepEqual(before, p.data.Tools) {
			return invalidEvent("unchanged tools")
		}
	case *SkillUpdatePayload:
		names := make([]string, len(v.Changes))
		for i, item := range v.Changes {
			names[i] = item.Name
		}
		if err := validateNames(names, nil); err != nil {
			return err
		}
		before := eventCopy(p.data.Skills)
		for _, item := range v.Changes {
			p.data.Skills[item.Name] = item.Active
		}
		if reflect.DeepEqual(before, p.data.Skills) {
			return invalidEvent("unchanged skills")
		}
	case *AnnotationAddPayload:
		if v.Text == "" {
			return invalidEvent("empty annotation")
		}
		if a := v.Anchor; a != nil {
			if (a.MessageID == "") == (a.Seq == 0) {
				return invalidEvent("anchor must select message or sequence")
			}
			if a.MessageID != "" && p.message(a.MessageID).ID == "" {
				return invalidEvent("unknown anchor message")
			}
			if a.Seq >= e.Seq {
				return invalidEvent("unknown anchor sequence")
			}
		}
	default:
		return invalidEvent("unsupported payload")
	}
	return nil
}
func knownEventType(t EventType) bool {
	switch t {
	case EventSessionCreate, EventMessageAppend, EventCompactionApply, EventConfigUpdate, EventStateUpdate, EventInferenceStart, EventInferenceFinish, EventToolStart, EventToolFinish, EventDelegationStart, EventDelegationFinish, EventPermissionRequest, EventPermissionDecide, EventHookFinish, EventInstructionUpdate, EventToolsetUpdate, EventSkillUpdate, EventAnnotationAdd:
		return true
	}
	return false
}
func (p *Projection) closeOperation(e Event, t EventType) error {
	start := p.event(e.SourceID)
	if start == nil || start.SessionID != e.SessionID || start.Type != t {
		return invalidEvent("finish/request reference family mismatch")
	}
	if p.data.EndByStart[start.ID] != "" {
		return invalidEvent("duplicate terminal result")
	}
	p.data.EndByStart[start.ID] = e.ID
	return nil
}
func validateTerminal(outcome EventOutcome, err *EventError, reason string, duration *int64) error {
	if duration != nil && *duration < 0 || len(reason) > 4096 {
		return invalidEvent("invalid duration or unbounded reason")
	}
	if err != nil && (err.Code == "" || err.Message == "" || len(err.Code) > 128 || len(err.Message) > 4096) {
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
func validateNames(upsert, remove []string) error {
	if len(upsert)+len(remove) == 0 {
		return invalidEvent("empty catalogue change")
	}
	seen := map[string]bool{}
	for _, names := range [][]string{upsert, remove} {
		for _, name := range names {
			if name == "" || seen[name] {
				return invalidEvent("duplicate/overlapping/empty catalogue name")
			}
			seen[name] = true
		}
	}
	return nil
}
func validateScope(s PermissionScope) error {
	if len(s.Resources) == 0 {
		return invalidEvent("empty permission scope")
	}
	return validateNames(s.Resources, nil)
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
	if s.ArchivedAt != nil && *s.ArchivedAt < 0 {
		return invalidEvent("invalid archive time")
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
func (p *Projection) addMessage(m ai.Message) error {
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
			calls[b.ToolCall.ID] = true
		}
		if b.Type == ai.BlockToolResult {
			if m.Role != ai.RoleUser || b.ToolResult == nil || b.ToolResult.ToolCallID == "" || results[b.ToolResult.ToolCallID] {
				return invalidEvent("invalid or duplicate native tool result")
			}
			results[b.ToolResult.ToolCallID] = true
		}
	}
	p.messageIndex[m.ID] = len(p.data.Messages)
	p.data.Messages = append(p.data.Messages, m)
	return nil
}
func (p *Projection) validateInput(ref InputRef) error {
	e := p.event(ref.EventID)
	if e == nil || ref.MessageID == "" {
		return invalidEvent("missing durable input reference")
	}
	if !slices.Contains(p.sourceMessageIDs(e.ID, EventValidationContext{}), ref.MessageID) {
		return invalidEvent("input message not owned by referenced event")
	}
	if ref.CallID != "" {
		for _, b := range p.message(ref.MessageID).Content {
			if b.Type == ai.BlockToolCall && b.ToolCall != nil && b.ToolCall.ID == ref.CallID {
				return nil
			}
		}
		return invalidEvent("input call missing")
	}
	return nil
}
func (p *Projection) sourceMessageIDs(id string, validation EventValidationContext) []string {
	if e := p.event(id); e != nil {
		switch v := e.Payload.(type) {
		case *MessageAppendPayload:
			ids := make([]string, len(v.Messages))
			for i, m := range v.Messages {
				ids[i] = m.ID
			}
			return ids
		case *CompactionApplyPayload:
			return []string{v.Summary.ID}
		}
	}
	return validation.ForeignEvents[id].MessageIDs
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
