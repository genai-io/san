package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Recorder adapts one core agent's admissions and observations to a typed session.
// Install all three admissions and RecordEvent together; observations alone are
// not durability fences. The final permission owner must call Decide before
// authorizing, after all policy and hook decisions have resolved.
// The runtime continues using LegacySession until the final format switch.
type Recorder struct {
	mu       sync.Mutex
	session  *Session
	provider string
	err      error
	turnID   string
	current  *Inference
	response *ai.Response
	admitted map[string]time.Time
	ended    map[string]recordedTool
}

type recordedTool struct {
	started bool
	time    int64
	finish  ToolFinishPayload
}

func NewRecorder(s *Session, provider string) *Recorder {
	return &Recorder{session: s, provider: provider, admitted: map[string]time.Time{}, ended: map[string]recordedTool{}}
}

func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.check(context.Background())
}

func (r *Recorder) check(ctx context.Context) error {
	if r.err != nil {
		return r.err
	}
	if err := r.session.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

func (r *Recorder) remember(err error) error {
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		if r.err == nil {
			r.err = err
		} else if !errors.Is(r.err, err) {
			r.err = errors.Join(r.err, err)
		}
	}
	return err
}

// InferenceAdmission commits the fully prepared request before dispatch, not at
// InferenceStart (which the SDK may emit even when no request is sent).
func (r *Recorder) InferenceAdmission(ctx context.Context, c sdkagent.InferenceAdmission) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(ctx); err != nil {
		return err
	}
	if r.current != nil {
		return r.remember(fmt.Errorf("previous inference has no admitted outcome"))
	}
	if r.turnID == "" {
		r.turnID = NewSessionID()
	}
	provider := r.provider
	if provider == "" {
		provider = string(c.Model.API)
	}
	inference, err := r.session.StartInference(ctx, InferenceOptions{Purpose: PurposeTurn, TurnID: r.turnID,
		Provider: provider, Model: c.Model, Request: c.Request})
	if err != nil {
		return r.remember(err)
	}
	r.current, r.response = inference, nil
	return nil
}

func permissionPayload(resolved perm.Resolution) PermissionDecidePayload {
	decision := PermissionDecidePayload{CallID: resolved.CallID, Decision: resolved.Decision,
		By: resolved.By, Scope: resolved.Scope, Reason: resolved.Reason}
	if resolved.Asked != nil {
		decision.Asked = &Asked{Options: resolved.Asked.Options, Detail: resolved.Asked.Detail, At: resolved.Asked.At}
	}
	return decision
}

// RecordPermission commits the final verdict, joining judge accounting when present.
func (r *Recorder) RecordPermission(ctx context.Context, resolved perm.Resolution) error {
	if resolved.Finish == nil {
		return r.Decide(ctx, permissionPayload(resolved))
	}
	r.mu.Lock()
	err := r.check(ctx)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	err = resolved.Finish(ctx, resolved)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.remember(err)
}

// Decide records policy's real authority, scope and presented question. It does
// not infer a user/config/grant decision from an SDK ToolStart notification.
func (r *Recorder) Decide(ctx context.Context, decision PermissionDecidePayload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(ctx); err != nil {
		return err
	}
	return r.remember(r.session.Append(ctx, Event{Payload: decision}))
}

func (r *Recorder) ToolAdmission(ctx context.Context, c sdkagent.ToolAdmission) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(ctx); err != nil {
		return err
	}
	p := r.session.Snapshot()
	call, ok := p.data.ToolCalls[c.Call.ID]
	if !ok || call.Decision != "allow" || call.Finish != 0 {
		return r.remember(fmt.Errorf("tool %s has no committed allow decision", c.Call.ID))
	}
	if _, duplicate := r.admitted[c.Call.ID]; duplicate {
		return r.remember(fmt.Errorf("tool %s was already admitted", c.Call.ID))
	}
	r.admitted[c.Call.ID] = time.Now()
	return nil
}

// OutputAdmission commits canonical output and its outcomes together, before
// publication. Raw ToolEnd details stay separate from filtered model content.
func (r *Recorder) OutputAdmission(ctx context.Context, c sdkagent.OutputAdmission) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(ctx); err != nil {
		return err
	}
	if c.Response != nil {
		if r.current == nil {
			return r.remember(fmt.Errorf("model output has no admitted inference"))
		}
		if err := r.current.Finish(ctx, c.Response, nil, Event{Payload: MessageAppendPayload{Messages: []ai.Message{c.Message}}}); err != nil {
			return r.remember(err)
		}
		r.current, r.response = nil, nil
		return nil
	}
	results := c.Message.ToolResults()
	if len(results) == 0 {
		return r.remember(fmt.Errorf("tool output has no tool results"))
	}
	err := r.session.write(ctx, func(p *Projection) ([]Event, error) {
		batch := []Event{{Payload: MessageAppendPayload{Messages: []ai.Message{c.Message}}}}
		for _, result := range results {
			call, ok := p.data.ToolCalls[result.ToolCallID]
			end, ended := r.ended[result.ToolCallID]
			if !ok || !ended {
				return nil, fmt.Errorf("tool output has no observed call %s", result.ToolCallID)
			}
			// A denied/invalid/abandoned call never ran. Its error result is
			// conversation, not evidence of an executed tool or a made-up gate.
			if end.started {
				batch = append(batch, Event{Time: end.time, Source: &EventRef{Seq: call.Seq}, Payload: end.finish})
			}
		}
		return batch, nil
	})
	if err != nil {
		return r.remember(err)
	}
	for _, result := range results {
		delete(r.ended, result.ToolCallID)
		delete(r.admitted, result.ToolCallID)
	}
	return nil
}

// RecordEvent observes facts that have no before-effect boundary of their own.
// Failures latch and stop the next admission. It never persists streamed deltas.
func (r *Recorder) RecordEvent(event core.Event) {
	if end, ok := event.(core.TurnEnded); ok {
		end.Result.Finalization.RecordError(r.Err())
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.check(context.Background()) != nil {
		// Already-accepted input and dispatched tools still report facts after an
		// admission error. Preserve those if the durable writer is healthy;
		// message still rejects generated content without output admission.
		switch event.(type) {
		case sdkagent.MessageAdded, sdkagent.ToolEnd, sdkagent.TurnEnd:
			if r.session.Err() != nil {
				return
			}
		default:
			return
		}
	}
	var err error
	switch e := event.(type) {
	case sdkagent.TurnStart:
		r.turnID = NewSessionID()
	case sdkagent.MessageAdded:
		err = r.message(e.Message)
	case sdkagent.MessagesReplaced:
		p := r.session.Snapshot()
		if !sameJSON(p.ActiveMessages(), e.Messages) {
			err = fmt.Errorf("context replacement must be committed through compaction or fork")
		}
	case sdkagent.InferenceEnd:
		if r.current != nil {
			if e.Err != nil {
				err = r.current.Finish(context.Background(), e.Response, e.Err)
				if err == nil {
					r.current, r.response = nil, nil
				}
			} else {
				r.response = e.Response
			}
		}
	case sdkagent.ToolEnd:
		err = r.toolEnd(e)
	case sdkagent.TurnEnd:
		if r.current != nil {
			failure := e.Err
			if failure == nil {
				failure = fmt.Errorf("inference ended without admitted output")
			}
			err = r.current.Finish(context.Background(), r.response, failure)
			if err == nil {
				r.current, r.response = nil, nil
			}
		}
		err = errors.Join(err, r.flushTools())
	case core.SystemChange:
		err = r.systemChange(e)
	case core.ToolsChange:
		err = r.toolsChange(e)
	}
	_ = r.remember(err)
}

func (r *Recorder) message(message ai.Message) error {
	return r.session.write(context.Background(), func(p *Projection) ([]Event, error) {
		if existing := p.message(message.ID); existing.ID != "" {
			if !sameJSON(existing, message) {
				return nil, fmt.Errorf("published message differs from committed output")
			}
			return nil, nil
		}
		if message.Role != ai.RoleUser || len(message.ToolResults()) > 0 {
			return nil, fmt.Errorf("generated message bypassed output admission")
		}
		return []Event{{Payload: MessageAppendPayload{Messages: []ai.Message{message}}}}, nil
	})
}

// flushTools preserves executed outcomes when a post-tool hook or cancellation
// prevents canonical tool-result output. It never invents conversation messages.
func (r *Recorder) flushTools() error {
	err := r.session.write(context.Background(), func(p *Projection) ([]Event, error) {
		ids := make([]string, 0, len(r.ended))
		for id, end := range r.ended {
			if end.started {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		var batch []Event
		for _, id := range ids {
			call, ok := p.data.ToolCalls[id]
			if !ok {
				return nil, fmt.Errorf("executed outcome has no recorded call %s", id)
			}
			if call.Finish == 0 {
				end := r.ended[id]
				batch = append(batch, Event{Time: end.time, Source: &EventRef{Seq: call.Seq}, Payload: end.finish})
			}
		}
		return batch, nil
	})
	if err == nil {
		clear(r.admitted)
		clear(r.ended)
	}
	return err
}

func (r *Recorder) toolEnd(end sdkagent.ToolEnd) error {
	if _, duplicate := r.ended[end.ID]; duplicate {
		return fmt.Errorf("duplicate tool outcome %s", end.ID)
	}
	recorded := recordedTool{started: end.Started, time: time.Now().UnixMilli()}
	if end.Started {
		started, ok := r.admitted[end.ID]
		if !ok {
			return fmt.Errorf("executed tool %s bypassed admission", end.ID)
		}
		duration := time.Since(started).Milliseconds()
		finish := ToolFinishPayload{CallID: end.ID, Outcome: OutcomeSuccess, DurationMS: &duration}
		if end.Err != nil {
			finish.Outcome = OutcomeFailure
			if errors.Is(end.Err, context.Canceled) || errors.Is(end.Err, context.DeadlineExceeded) {
				finish.Outcome = OutcomeCancelled
			}
			finish.Reason = boundedReason(end.Err.Error())
			if finish.Reason == "" {
				finish.Reason = "tool failed"
			}
			finish.Error = &EventError{Message: finish.Reason, Retryable: ai.IsRetryable(end.Err)}
		}
		switch details := end.Result.Details.(type) {
		case toolresult.FileChangeDetails, toolresult.BashDetails:
			raw, err := json.Marshal(details)
			if err != nil {
				return err
			}
			finish.Details = raw
		case toolresult.AgentDetails:
			raw, err := json.Marshal(details)
			if err != nil {
				return err
			}
			finish.Details, finish.ChildSession = raw, details.Identity.SessionID
		}
		recorded.finish = finish
	}
	r.ended[end.ID] = recorded
	return nil
}

func recorderCause(cause string) string {
	if cause == "system:init" || cause == "tools:init" {
		return "boot"
	}
	if cause == "" {
		return "runtime"
	}
	return cause
}

func (r *Recorder) systemChange(change core.SystemChange) error {
	if change.Name == "" {
		return fmt.Errorf("empty instruction name")
	}
	return r.session.write(context.Background(), func(p *Projection) ([]Event, error) {
		old, exists := p.data.Instructions[change.Name]
		update := InstructionsUpdatePayload{Cause: recorderCause(change.Caller)}
		if change.Removed || change.Content == "" {
			if !exists {
				return nil, nil
			}
			update.Remove = []string{change.Name}
		} else {
			next := InstructionSection{Content: change.Content, Slot: change.Slot}
			if exists && old == next {
				return nil, nil
			}
			update.Set = map[string]InstructionSection{change.Name: next}
		}
		return []Event{{Payload: update}}, nil
	})
}

func (r *Recorder) toolsChange(change core.ToolsChange) error {
	return r.session.write(context.Background(), func(p *Projection) ([]Event, error) {
		name := change.Schema.Name
		if change.Removed {
			name = change.Name
		}
		if name == "" {
			return nil, fmt.Errorf("empty tool name")
		}
		old, exists := p.data.Tools[name]
		update := ToolsetUpdatePayload{Cause: recorderCause(change.Caller)}
		if change.Removed {
			if !exists {
				return nil, nil
			}
			update.Remove = []string{name}
		} else {
			if exists && sameJSON(old, change.Schema) {
				return nil, nil
			}
			update.Set = map[string]ai.Schema{name: change.Schema}
		}
		return []Event{{Payload: update}}, nil
	})
}

func (r *Recorder) RecordSkill(ctx context.Context, name, state, cause string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(ctx); err != nil {
		return err
	}
	return r.remember(r.session.write(ctx, func(p *Projection) ([]Event, error) {
		if name == "" || (state != SkillDisabled && state != SkillEnabled && state != SkillActive) {
			return nil, fmt.Errorf("invalid skill state")
		}
		if p.data.Skills[name] == state {
			return nil, nil
		}
		return []Event{{Payload: SkillsUpdatePayload{Set: map[string]string{name: state}, Cause: recorderCause(cause)}}}, nil
	}))
}

// RecordCatalogues reconciles complete current runtime catalogues in one durable
// batch. Missing entries are removed; unchanged snapshots produce no events.
func (r *Recorder) RecordCatalogues(ctx context.Context, instructions []NamedInstruction, tools []ai.Schema, skills map[string]string, cause string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(ctx); err != nil {
		return err
	}
	if cause == "" {
		return r.remember(fmt.Errorf("missing catalogue cause"))
	}
	ins := make(map[string]InstructionSection, len(instructions))
	for _, entry := range instructions {
		if _, duplicate := ins[entry.Name]; duplicate || entry.Name == "" || entry.Content == "" || entry.Slot < 0 {
			return r.remember(fmt.Errorf("invalid or duplicate instruction %q", entry.Name))
		}
		ins[entry.Name] = entry.InstructionSection
	}
	ts := make(map[string]ai.Schema, len(tools))
	for _, schema := range tools {
		if _, duplicate := ts[schema.Name]; duplicate || schema.Name == "" {
			return r.remember(fmt.Errorf("invalid or duplicate tool %q", schema.Name))
		}
		ts[schema.Name] = schema
	}
	ss := make(map[string]string, len(skills))
	for name, state := range skills {
		if name == "" || (state != SkillDisabled && state != SkillEnabled && state != SkillActive) {
			return r.remember(fmt.Errorf("invalid skill %q", name))
		}
		ss[name] = state
	}
	// Round-trip schemas before diffing: detach nested caller-owned definitions
	// and reject definitions that cannot cross the native JSON boundary.
	raw, err := json.Marshal(ts)
	if err != nil {
		return r.remember(err)
	}
	if err := strictEventJSON(raw, &ts); err != nil {
		return r.remember(err)
	}
	return r.remember(r.session.write(ctx, func(p *Projection) ([]Event, error) {
		var batch []Event
		setIns, removeIns := catalogueDiff(p.data.Instructions, ins)
		if len(setIns)+len(removeIns) > 0 {
			batch = append(batch, Event{Payload: InstructionsUpdatePayload{Set: setIns, Remove: removeIns, Cause: cause}})
		}
		setTools, removeTools := catalogueDiff(p.data.Tools, ts)
		if len(setTools)+len(removeTools) > 0 {
			batch = append(batch, Event{Payload: ToolsetUpdatePayload{Set: setTools, Remove: removeTools, Cause: cause}})
		}
		setSkills, removeSkills := catalogueDiff(p.data.Skills, ss)
		if len(setSkills)+len(removeSkills) > 0 {
			batch = append(batch, Event{Payload: SkillsUpdatePayload{Set: setSkills, Remove: removeSkills, Cause: cause}})
		}
		return batch, nil
	}))
}

func catalogueDiff[T any](old, next map[string]T) (map[string]T, []string) {
	set := map[string]T{}
	var remove []string
	for name, value := range next {
		if previous, exists := old[name]; !exists || !sameJSON(previous, value) {
			set[name] = value
		}
	}
	for name := range old {
		if _, exists := next[name]; !exists {
			remove = append(remove, name)
		}
	}
	sort.Strings(remove)
	return set, remove
}

// RecordHook is an explicit durable completion operation, not the hook engine's
// nonblocking audit callback. An async launch is not a completed hook.
func (r *Recorder) RecordHook(ctx context.Context, audit hook.HookFiredAudit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(ctx); err != nil {
		return err
	}
	if audit.Outcome == "async" {
		return nil
	}
	duration := audit.Duration.Milliseconds()
	return r.remember(r.session.Append(ctx, Event{Payload: HookFinishPayload{Hook: audit.Source, Trigger: audit.Event,
		Matcher: audit.Matcher, Outcome: audit.Outcome, DurationMS: &duration, Reason: boundedReason(audit.Reason)}}))
}

func (r *Recorder) RecordState(ctx context.Context, update SessionUpdatePayload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(ctx); err != nil {
		return err
	}
	return r.remember(r.session.write(ctx, func(p *Projection) ([]Event, error) {
		if update.Config == nil && update.State == nil {
			return nil, fmt.Errorf("empty session update")
		}
		config, state := p.data.Config, p.data.State
		if update.Config != nil {
			applyConfigPatch(&config, *update.Config)
		}
		if update.State != nil {
			applyStatePatch(&state, *update.State)
		}
		if reflect.DeepEqual(config, p.data.Config) && reflect.DeepEqual(state, p.data.State) {
			return nil, nil
		}
		return []Event{{Payload: update}}, nil
	}))
}
