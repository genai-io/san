package session

// This file defines unintegrated event groundwork. It is not the legacy
// transcript format and is deliberately not connected to Session's writer.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/genai-io/sdk-go/pkg/ai"
)

type EventType string

const (
	EventSessionCreate     EventType = "session.create"
	EventMessageAppend     EventType = "message.append"
	EventCompactionApply   EventType = "compaction.apply"
	EventConfigUpdate      EventType = "config.update"
	EventStateUpdate       EventType = "state.update"
	EventInferenceStart    EventType = "inference.start"
	EventInferenceFinish   EventType = "inference.finish"
	EventToolStart         EventType = "tool.start"
	EventToolFinish        EventType = "tool.finish"
	EventDelegationStart   EventType = "delegation.start"
	EventDelegationFinish  EventType = "delegation.finish"
	EventPermissionRequest EventType = "permission.request"
	EventPermissionDecide  EventType = "permission.decide"
	EventHookFinish        EventType = "hook.finish"
	EventInstructionUpdate EventType = "instruction.update"
	EventToolsetUpdate     EventType = "toolset.update"
	EventSkillUpdate       EventType = "skill.update"
	EventAnnotationAdd     EventType = "annotation.add"
)

type EventOutcome string

const (
	OutcomeSuccess   EventOutcome = "success"
	OutcomeFailure   EventOutcome = "failure"
	OutcomeCancelled EventOutcome = "cancelled"
)

type Event struct {
	ID            string       `json:"id"`
	SessionID     string       `json:"session_id"`
	Seq           uint64       `json:"seq"`
	Type          EventType    `json:"type"`
	SchemaVersion int          `json:"schema_version"`
	CreatedAt     int64        `json:"created_at"`
	SourceID      string       `json:"source_id,omitempty"`
	Outcome       EventOutcome `json:"outcome,omitempty"`
	Payload       EventPayload `json:"payload"`
}

// EventPayload is closed to the eighteen supported payload families.
type EventPayload interface{ eventType() EventType }

// PatchField distinguishes an absent field, a JSON null (clear), and a value.
// Use omitzero on containing fields; false, zero and empty values remain sets.
type PatchField[T any] struct {
	Present bool
	Value   *T
}

func SetField[T any](v T) PatchField[T]              { return PatchField[T]{true, &v} }
func ClearField[T any]() PatchField[T]               { return PatchField[T]{Present: true} }
func (p PatchField[T]) MarshalJSON() ([]byte, error) { return json.Marshal(p.Value) }
func (p *PatchField[T]) UnmarshalJSON(b []byte) error {
	p.Present = true
	return strictEventJSON(b, &p.Value)
}

type GenerationOptions struct {
	Version       int       `json:"version"`
	MaxTokens     *int      `json:"max_tokens,omitempty"`
	Temperature   *float64  `json:"temperature,omitempty"`
	Effort        ai.Effort `json:"effort,omitempty"`
	StopSequences []string  `json:"stop_sequences,omitempty"`
}
type AutopilotPolicy struct {
	Version  int `json:"version"`
	MaxTurns int `json:"max_turns"`
}
type EventConfig struct {
	Provider          *string            `json:"provider,omitempty"`
	Model             *string            `json:"model,omitempty"`
	GenerationOptions *GenerationOptions `json:"generation_options,omitempty"`
	Mode              *string            `json:"mode,omitempty"`
	AutopilotPolicy   *AutopilotPolicy   `json:"autopilot_policy,omitempty"`
}
type ConfigPatch struct {
	Provider          PatchField[string]            `json:"provider,omitzero"`
	Model             PatchField[string]            `json:"model,omitzero"`
	GenerationOptions PatchField[GenerationOptions] `json:"generation_options,omitzero"`
	Mode              PatchField[string]            `json:"mode,omitzero"`
	AutopilotPolicy   PatchField[AutopilotPolicy]   `json:"autopilot_policy,omitzero"`
}
type EventTodo struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"`
}
type AutopilotProgress struct {
	CompletedTurns int    `json:"completed_turns"`
	Status         string `json:"status"`
}
type GroupHint struct {
	GroupID  string `json:"group_id"`
	MemberID string `json:"member_id"`
}
type EventState struct {
	Title             *string            `json:"title,omitempty"`
	ArchivedAt        *int64             `json:"archived_at,omitempty"`
	Pinned            *bool              `json:"pinned,omitempty"`
	Todos             *[]EventTodo       `json:"todos,omitempty"`
	AutopilotProgress *AutopilotProgress `json:"autopilot_progress,omitempty"`
	ExecutionCWD      *string            `json:"execution_cwd,omitempty"`
	GroupHint         *GroupHint         `json:"group_hint,omitempty"`
}
type StatePatch struct {
	Title             PatchField[string]            `json:"title,omitzero"`
	ArchivedAt        PatchField[int64]             `json:"archived_at,omitzero"`
	Pinned            PatchField[bool]              `json:"pinned,omitzero"`
	Todos             PatchField[[]EventTodo]       `json:"todos,omitzero"`
	AutopilotProgress PatchField[AutopilotProgress] `json:"autopilot_progress,omitzero"`
	ExecutionCWD      PatchField[string]            `json:"execution_cwd,omitzero"`
	GroupHint         PatchField[GroupHint]         `json:"group_hint,omitzero"`
}
type SessionCreatePayload struct {
	ProjectID       string       `json:"project_id"`
	Workspace       string       `json:"workspace"`
	InitialCWD      string       `json:"initial_cwd"`
	AgentID         string       `json:"agent_id"`
	Sidechain       bool         `json:"sidechain"`
	ParentSessionID string       `json:"parent_session_id,omitempty"`
	ForkSourceID    string       `json:"fork_source_id,omitempty"`
	ForkSourceSeq   uint64       `json:"fork_source_seq,omitempty"`
	InitialConfig   *EventConfig `json:"initial_config,omitempty"`
}
type MessageAppendPayload struct {
	Messages         []ai.Message `json:"messages"`
	SourceMessageIDs []string     `json:"source_message_ids,omitempty"`
}
type CompactionApplyPayload struct {
	SourceSeq          uint64     `json:"source_seq"`
	Summary            ai.Message `json:"summary"`
	RetainedMessageIDs []string   `json:"retained_message_ids"`
}
type ConfigUpdatePayload struct {
	Patch ConfigPatch `json:"patch"`
}
type StateUpdatePayload struct {
	Patch StatePatch `json:"patch"`
}
type InferenceStartPayload struct {
	TurnID            string      `json:"turn_id"`
	InputMessageIDs   []string    `json:"input_message_ids"`
	RequestConfig     EventConfig `json:"request_config"`
	InstructionDigest string      `json:"instruction_digest"`
	ToolsetDigest     string      `json:"toolset_digest"`
}
type EventError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type InferenceFinishPayload struct {
	StopReason string      `json:"stop_reason,omitempty"`
	Usage      *ai.Usage   `json:"usage,omitempty"`
	DurationMS *int64      `json:"duration_ms,omitempty"`
	Error      *EventError `json:"error,omitempty"`
	Reason     string      `json:"reason,omitempty"`
}

// InputRef addresses immutable content in a local message event. CallID, when
// present, selects the native invocation rather than the whole message.
type InputRef struct {
	EventID   string `json:"event_id"`
	MessageID string `json:"message_id"`
	CallID    string `json:"call_id,omitempty"`
}
type ToolStartPayload struct {
	CallID   string   `json:"call_id"`
	InputRef InputRef `json:"input_ref"`
}
type ToolExecutionDetails struct {
	ExitCode    *int   `json:"exit_code,omitempty"`
	DisplayText string `json:"display_text,omitempty"`
}
type ToolFinishPayload struct {
	CallID     string                `json:"call_id"`
	DurationMS *int64                `json:"duration_ms,omitempty"`
	Error      *EventError           `json:"error,omitempty"`
	Reason     string                `json:"reason,omitempty"`
	Details    *ToolExecutionDetails `json:"details,omitempty"`
}
type DelegationStartPayload struct {
	ChildSessionID string   `json:"child_session_id"`
	InputRef       InputRef `json:"input_ref"`
}
type DelegationFinishPayload struct {
	ResultEventID string      `json:"result_event_id,omitempty"`
	Error         *EventError `json:"error,omitempty"`
	Reason        string      `json:"reason,omitempty"`
}
type PermissionScope struct {
	Resources []string `json:"resources"`
}
type PermissionRequestPayload struct {
	OperationRef string          `json:"operation_ref"`
	Scope        PermissionScope `json:"scope"`
}
type PermissionDecidePayload struct {
	Decision string          `json:"decision"`
	Scope    PermissionScope `json:"scope"`
	Reason   string          `json:"reason,omitempty"`
}
type HookFinishPayload struct {
	HookName   string   `json:"hook_name"`
	Trigger    string   `json:"trigger"`
	DurationMS *int64   `json:"duration_ms,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	EffectRefs []string `json:"effect_refs,omitempty"`
}
type InstructionSection struct {
	Name string `json:"name"`
	Text string `json:"text"`
}
type InstructionUpdatePayload struct {
	Upsert []InstructionSection `json:"upsert"`
	Remove []string             `json:"remove"`
	Source string               `json:"source,omitempty"`
}
type ToolsetUpdatePayload struct {
	Upsert []ai.Schema `json:"upsert"`
	Remove []string    `json:"remove"`
	Source string      `json:"source,omitempty"`
}
type SkillActivation struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}
type SkillUpdatePayload struct {
	Changes []SkillActivation `json:"changes"`
	Source  string            `json:"source,omitempty"`
}
type AnnotationAnchor struct {
	MessageID string `json:"message_id,omitempty"`
	Seq       uint64 `json:"seq,omitempty"`
}
type AnnotationAddPayload struct {
	Text   string            `json:"text"`
	Anchor *AnnotationAnchor `json:"anchor,omitempty"`
}

func (SessionCreatePayload) eventType() EventType     { return EventSessionCreate }
func (MessageAppendPayload) eventType() EventType     { return EventMessageAppend }
func (CompactionApplyPayload) eventType() EventType   { return EventCompactionApply }
func (ConfigUpdatePayload) eventType() EventType      { return EventConfigUpdate }
func (StateUpdatePayload) eventType() EventType       { return EventStateUpdate }
func (InferenceStartPayload) eventType() EventType    { return EventInferenceStart }
func (InferenceFinishPayload) eventType() EventType   { return EventInferenceFinish }
func (ToolStartPayload) eventType() EventType         { return EventToolStart }
func (ToolFinishPayload) eventType() EventType        { return EventToolFinish }
func (DelegationStartPayload) eventType() EventType   { return EventDelegationStart }
func (DelegationFinishPayload) eventType() EventType  { return EventDelegationFinish }
func (PermissionRequestPayload) eventType() EventType { return EventPermissionRequest }
func (PermissionDecidePayload) eventType() EventType  { return EventPermissionDecide }
func (HookFinishPayload) eventType() EventType        { return EventHookFinish }
func (InstructionUpdatePayload) eventType() EventType { return EventInstructionUpdate }
func (ToolsetUpdatePayload) eventType() EventType     { return EventToolsetUpdate }
func (SkillUpdatePayload) eventType() EventType       { return EventSkillUpdate }
func (AnnotationAddPayload) eventType() EventType     { return EventAnnotationAdd }

func strictEventJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	d.UseNumber() // Native schema const/enum integers must not round through float64.
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

// checkEventJSONKeys rejects ambiguous duplicate object members without rewriting
// native schema definitions, numbers or content. Native arbitrary JSON remains
// opaque to product-field validation; object keys must still be unambiguous.
func checkEventJSONKeys(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate or invalid JSON member %q", name)
				}
				seen[name] = true
				if err = value(); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func requiredEventFields(b []byte, typ reflect.Type) error {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		parts := strings.Split(tag, ",")
		if tag == "" || parts[0] == "-" || slicesContainsOptionalTag(parts[1:]) {
			continue
		}
		raw, ok := fields[parts[0]]
		if !ok {
			return fmt.Errorf("missing required field %q", parts[0])
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && field.Type.Kind() != reflect.Slice && field.Type.Kind() != reflect.Map {
			return fmt.Errorf("null required field %q", parts[0])
		}
	}
	return nil
}
func slicesContainsOptionalTag(tags []string) bool {
	for _, tag := range tags {
		if tag == "omitempty" || tag == "omitzero" {
			return true
		}
	}
	return false
}

func (e *Event) UnmarshalJSON(b []byte) error {
	if err := checkEventJSONKeys(b); err != nil {
		return err
	}
	if err := requiredEventFields(b, reflect.TypeFor[Event]()); err != nil {
		return err
	}
	type envelope Event
	var wire struct {
		*envelope
		Payload json.RawMessage `json:"payload"`
	}
	var next Event
	wire.envelope = (*envelope)(&next)
	if err := strictEventJSON(b, &wire); err != nil {
		return err
	}
	var p EventPayload
	switch next.Type {
	case EventSessionCreate:
		p = &SessionCreatePayload{}
	case EventMessageAppend:
		p = &MessageAppendPayload{}
	case EventCompactionApply:
		p = &CompactionApplyPayload{}
	case EventConfigUpdate:
		p = &ConfigUpdatePayload{}
	case EventStateUpdate:
		p = &StateUpdatePayload{}
	case EventInferenceStart:
		p = &InferenceStartPayload{}
	case EventInferenceFinish:
		p = &InferenceFinishPayload{}
	case EventToolStart:
		p = &ToolStartPayload{}
	case EventToolFinish:
		p = &ToolFinishPayload{}
	case EventDelegationStart:
		p = &DelegationStartPayload{}
	case EventDelegationFinish:
		p = &DelegationFinishPayload{}
	case EventPermissionRequest:
		p = &PermissionRequestPayload{}
	case EventPermissionDecide:
		p = &PermissionDecidePayload{}
	case EventHookFinish:
		p = &HookFinishPayload{}
	case EventInstructionUpdate:
		p = &InstructionUpdatePayload{}
	case EventToolsetUpdate:
		p = &ToolsetUpdatePayload{}
	case EventSkillUpdate:
		p = &SkillUpdatePayload{}
	case EventAnnotationAdd:
		p = &AnnotationAddPayload{}
	default:
		return fmt.Errorf("unsupported event type %q", next.Type)
	}
	if len(wire.Payload) == 0 || bytes.Equal(bytes.TrimSpace(wire.Payload), []byte("null")) {
		return fmt.Errorf("missing payload")
	}
	if err := requiredEventFields(wire.Payload, reflect.TypeOf(p)); err != nil {
		return err
	}
	if err := strictEventJSON(wire.Payload, p); err != nil {
		return err
	}
	next.Payload = p
	*e = next
	return nil
}
