package session

// Typed events persisted by Store and Session. Runtime producers still use
// LegacySession until the single format switch (see the delivery plan in
// docs/design/proposal-0005-session-management.md).
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
	EventSessionCreate      EventType = "session.create"
	EventSessionUpdate      EventType = "session.update"
	EventMessageAppend      EventType = "message.append"
	EventContextCompact     EventType = "context.compact"
	EventInferenceStart     EventType = "inference.start"
	EventInferenceFinish    EventType = "inference.finish"
	EventPermissionDecide   EventType = "permission.decide"
	EventToolFinish         EventType = "tool.finish"
	EventInstructionsUpdate EventType = "instructions.update"
	EventToolsetUpdate      EventType = "toolset.update"
	EventSkillsUpdate       EventType = "skills.update"
	EventHookFinish         EventType = "hook.finish"
	EventAnnotationAdd      EventType = "annotation.add"
)

type EventOutcome string

const (
	OutcomeSuccess   EventOutcome = "success"
	OutcomeFailure   EventOutcome = "failure"
	OutcomeCancelled EventOutcome = "cancelled"
)

// EventRef names another event: in this session when Session is empty.
type EventRef struct {
	Session string `json:"session,omitempty"`
	Seq     uint64 `json:"seq"`
}

// Event is identified by its position, Seq, within its session. Provenance
// links use Source; nothing else is allocated or globally unique.
type Event struct {
	Seq           uint64       `json:"seq"`
	Type          EventType    `json:"type"`
	SchemaVersion int          `json:"v"`
	Time          int64        `json:"time"`
	Source        *EventRef    `json:"source,omitempty"`
	Payload       EventPayload `json:"payload"`
}

// EventPayload is closed to the thirteen supported payload families.
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
	Version        int               `json:"version"`
	MaxTokens      *int              `json:"max_tokens,omitempty"`
	Temperature    *float64          `json:"temperature,omitempty"`
	Effort         ai.Effort         `json:"effort,omitempty"`
	StopSequences  []string          `json:"stop_sequences,omitempty"`
	ToolChoice     ai.ToolChoice     `json:"tool_choice,omitzero"`
	CacheRetention ai.CacheRetention `json:"cache_retention,omitempty"`
	Schema         *ai.Schema        `json:"schema,omitempty"`
	SamplingParams map[string]any    `json:"sampling_params,omitempty"`
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

// EventState is session state recorded as facts. Archive, pin and a renamed
// title are user intent and live in attrs, never here.
type EventState struct {
	Title             *string            `json:"title,omitempty"`
	Todos             *[]EventTodo       `json:"todos,omitempty"`
	AutopilotProgress *AutopilotProgress `json:"autopilot_progress,omitempty"`
	ExecutionCWD      *string            `json:"execution_cwd,omitempty"`
	GroupHint         *GroupHint         `json:"group_hint,omitempty"`
}
type StatePatch struct {
	Title             PatchField[string]            `json:"title,omitzero"`
	Todos             PatchField[[]EventTodo]       `json:"todos,omitzero"`
	AutopilotProgress PatchField[AutopilotProgress] `json:"autopilot_progress,omitzero"`
	ExecutionCWD      PatchField[string]            `json:"execution_cwd,omitzero"`
	GroupHint         PatchField[GroupHint]         `json:"group_hint,omitzero"`
}

// ParentLink says which session, and optionally which tool call, started this one.
type ParentLink struct {
	Session string `json:"session"`
	CallID  string `json:"call_id,omitempty"`
}
type ForkLink struct {
	Session string `json:"session"`
	Seq     uint64 `json:"seq"`
}
type SessionCreatePayload struct {
	ProjectID  string       `json:"project"`
	Workspace  string       `json:"workspace"`
	InitialCWD string       `json:"initial_cwd"`
	AgentID    string       `json:"agent_id"`
	Config     *EventConfig `json:"config,omitempty"`
	Parent     *ParentLink  `json:"parent,omitempty"`
	Fork       *ForkLink    `json:"fork,omitempty"`
}
type SessionUpdatePayload struct {
	Config *ConfigPatch `json:"config,omitempty"`
	State  *StatePatch  `json:"state,omitempty"`
}
type MessageAppendPayload struct {
	Messages []ai.Message `json:"messages"`
}

// ContextCompactPayload replaces the active context with Summary plus every
// message appended after Through. History keeps everything.
type ContextCompactPayload struct {
	Summary ai.Message `json:"summary"`
	Through uint64     `json:"through"`
}

// Purposes name why a model call was made; each has one usual input kind and
// one event that holds its result.
const (
	PurposeTurn       = "turn"
	PurposeBtw        = "btw"
	PurposeRecap      = "recap"
	PurposeSuggest    = "suggest"
	PurposeCompaction = "compaction"
	PurposeJudge      = "judge"
	PurposeHook       = "hook"
	PurposeAutopilot  = "autopilot"
	PurposeGroup      = "group"
	PurposeRecall     = "recall"
)

// ConversationInput: the request is this session's own context, instructions and
// toolset as projected just before the event, followed by Append for this call only.
type ConversationInput struct {
	Append []ai.Message `json:"append,omitempty"`
}

// InferenceInput is exactly one of Conversation or Standalone. Standalone is the
// digest of an exact copy of the request stored once as a blob.
type InferenceInput struct {
	Conversation *ConversationInput `json:"conversation,omitempty"`
	Standalone   string             `json:"standalone,omitempty"`
}
type InferenceStartPayload struct {
	Purpose            string         `json:"purpose"`
	TurnID             string         `json:"turn_id,omitempty"`
	Request            EventConfig    `json:"request"`
	Input              InferenceInput `json:"input"`
	InstructionsDigest string         `json:"instructions_digest,omitempty"`
	ToolsetDigest      string         `json:"toolset_digest,omitempty"`
}
type EventError struct {
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}
type InferenceFinishPayload struct {
	Outcome    EventOutcome `json:"outcome"`
	StopReason string       `json:"stop_reason,omitempty"`
	Usage      *ai.Usage    `json:"usage,omitempty"`
	DurationMS *int64       `json:"duration_ms,omitempty"`
	Error      *EventError  `json:"error,omitempty"`
	Reason     string       `json:"reason,omitempty"`
}

// Permission deciders. Grant is an earlier session-scoped approval and is never
// restored on resume.
const (
	ByConfig = "config"
	ByMode   = "mode"
	ByGrant  = "grant"
	ByUser   = "user"
	ByHook   = "hook"
	ByJudge  = "judge"
)

// Asked keeps what the person was shown when a decision needed asking.
type Asked struct {
	Options []string        `json:"options,omitempty"`
	Detail  json.RawMessage `json:"detail,omitempty"`
	At      int64           `json:"at,omitempty"`
}
type PermissionDecidePayload struct {
	CallID   string `json:"call_id"`
	Decision string `json:"decision"`
	By       string `json:"by"`
	Scope    string `json:"scope,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Asked    *Asked `json:"asked,omitempty"`
}
type ToolFinishPayload struct {
	CallID       string          `json:"call_id"`
	Outcome      EventOutcome    `json:"outcome"`
	DurationMS   *int64          `json:"duration_ms,omitempty"`
	Error        *EventError     `json:"error,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	Details      json.RawMessage `json:"details,omitempty"`
	ChildSession string          `json:"child_session,omitempty"`
}

// Catalogue updates name each entry once across Set and Remove; Cause says who
// changed it (boot, mcp:<server>, plugin:<name>, user:<command>, resume).
type InstructionSection struct {
	Content string `json:"content"`
	Slot    int    `json:"slot,omitempty"`
}
type InstructionsUpdatePayload struct {
	Set    map[string]InstructionSection `json:"set,omitempty"`
	Remove []string                      `json:"remove,omitempty"`
	Cause  string                        `json:"cause"`
}
type ToolsetUpdatePayload struct {
	Set    map[string]ai.Schema `json:"set,omitempty"`
	Remove []string             `json:"remove,omitempty"`
	Cause  string               `json:"cause"`
}

const (
	SkillDisabled = "disabled"
	SkillEnabled  = "enabled"
	SkillActive   = "active"
)

type SkillsUpdatePayload struct {
	Set    map[string]string `json:"set,omitempty"`
	Remove []string          `json:"remove,omitempty"`
	Cause  string            `json:"cause"`
}

const (
	HookRan     = "ran"
	HookBlocked = "blocked"
	HookError   = "error"
)

type HookFinishPayload struct {
	Hook       string `json:"hook"`
	Trigger    string `json:"trigger"`
	Matcher    string `json:"matcher,omitempty"`
	Outcome    string `json:"outcome"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// AnnotationAddPayload is shown to the person and never to the model.
type AnnotationAddPayload struct {
	Text         string `json:"text"`
	ByAgent      bool   `json:"by_agent,omitempty"`
	AfterMessage string `json:"after_message,omitempty"`
}

func (SessionCreatePayload) eventType() EventType      { return EventSessionCreate }
func (SessionUpdatePayload) eventType() EventType      { return EventSessionUpdate }
func (MessageAppendPayload) eventType() EventType      { return EventMessageAppend }
func (ContextCompactPayload) eventType() EventType     { return EventContextCompact }
func (InferenceStartPayload) eventType() EventType     { return EventInferenceStart }
func (InferenceFinishPayload) eventType() EventType    { return EventInferenceFinish }
func (PermissionDecidePayload) eventType() EventType   { return EventPermissionDecide }
func (ToolFinishPayload) eventType() EventType         { return EventToolFinish }
func (InstructionsUpdatePayload) eventType() EventType { return EventInstructionsUpdate }
func (ToolsetUpdatePayload) eventType() EventType      { return EventToolsetUpdate }
func (SkillsUpdatePayload) eventType() EventType       { return EventSkillsUpdate }
func (HookFinishPayload) eventType() EventType         { return EventHookFinish }
func (AnnotationAddPayload) eventType() EventType      { return EventAnnotationAdd }

// newEventPayload returns an empty payload for a known type, or nil.
func newEventPayload(t EventType) EventPayload {
	switch t {
	case EventSessionCreate:
		return &SessionCreatePayload{}
	case EventSessionUpdate:
		return &SessionUpdatePayload{}
	case EventMessageAppend:
		return &MessageAppendPayload{}
	case EventContextCompact:
		return &ContextCompactPayload{}
	case EventInferenceStart:
		return &InferenceStartPayload{}
	case EventInferenceFinish:
		return &InferenceFinishPayload{}
	case EventPermissionDecide:
		return &PermissionDecidePayload{}
	case EventToolFinish:
		return &ToolFinishPayload{}
	case EventInstructionsUpdate:
		return &InstructionsUpdatePayload{}
	case EventToolsetUpdate:
		return &ToolsetUpdatePayload{}
	case EventSkillsUpdate:
		return &SkillsUpdatePayload{}
	case EventHookFinish:
		return &HookFinishPayload{}
	case EventAnnotationAdd:
		return &AnnotationAddPayload{}
	}
	return nil
}
func knownEventType(t EventType) bool { return newEventPayload(t) != nil }

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
	p := newEventPayload(next.Type)
	if p == nil {
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
