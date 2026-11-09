package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// InferenceOptions describes the resolved request admitted at the model boundary.
type InferenceOptions struct {
	Purpose, TurnID, Provider string
	Model                     ai.Model
	Request                   *ai.Request
	Append                    []ai.Message
}

// Inference is an immutable handle to one committed local model call.
type Inference struct {
	owner   *Session
	ref     EventRef
	started time.Time
	through uint64
}

func (i *Inference) Ref() EventRef { return i.ref }

func (s *Session) StartInference(ctx context.Context, opts InferenceOptions) (*Inference, error) {
	if opts.Request == nil {
		return nil, fmt.Errorf("nil inference request")
	}
	var handle *Inference
	err := s.write(ctx, func(p *Projection) ([]Event, error) {
		r := opts.Request
		max := r.MaxTokens
		model := opts.Model.ID
		generation := &GenerationOptions{
			Version: 1, MaxTokens: &max, Temperature: r.Temperature, Effort: r.Effort,
			StopSequences: r.StopSequences, ToolChoice: r.ToolChoice,
			CacheRetention: r.CacheRetention, Schema: r.Schema, SamplingParams: r.SamplingParams,
		}
		start := InferenceStartPayload{Purpose: opts.Purpose, TurnID: opts.TurnID,
			Request: EventConfig{Provider: &opts.Provider, Model: &model, GenerationOptions: generation}}
		expected := conversationRequest(p, opts.Append)
		if r.System == expected.System && sameJSON(r.Messages, expected.Messages) && sameJSON(r.Tools, expected.Tools) {
			start.Input.Conversation = &ConversationInput{Append: opts.Append}
			start.InstructionsDigest, start.ToolsetDigest = p.CatalogueDigests()
		} else {
			digest, err := s.requests().PutRequest(StandaloneRequest{System: r.System, Messages: r.Messages, Tools: r.Tools})
			if err != nil {
				return nil, err
			}
			start.Input.Standalone = digest
		}
		now := time.Now()
		through := uint64(len(p.data.Events))
		handle = &Inference{owner: s, ref: EventRef{Seq: through + 1}, started: now, through: through}
		return []Event{{Time: now.UnixMilli(), Payload: start}}, nil
	})
	if err != nil {
		return nil, err
	}
	return handle, nil
}

func sameJSON(a, b any) bool {
	// Empty and omitted slices have the same native request semantics.
	av, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bv, err := json.Marshal(b)
	if err != nil {
		return false
	}
	if (string(av) == "null" && string(bv) == "[]") || (string(av) == "[]" && string(bv) == "null") {
		return true
	}
	return string(av) == string(bv)
}

func conversationRequest(p *Projection, extra []ai.Message) *ai.Request {
	system := core.NewSystem()
	for _, e := range p.data.Events {
		update, ok := e.Payload.(*InstructionsUpdatePayload)
		if !ok {
			continue
		}
		for _, name := range update.Remove {
			system.Drop(name, update.Cause)
		}
		keys := make([]string, 0, len(update.Set))
		for name := range update.Set {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			section := update.Set[name]
			system.Use(core.Section{Name: name, Slot: core.Slot(section.Slot), Render: func() string { return section.Content }}, update.Cause)
		}
	}
	messages := append(p.ActiveMessages(), extra...)
	return &ai.Request{System: system.Prompt(), Messages: ai.Repair(messages), Tools: p.Tools()}
}

// Finish commits caller-owned results and the outcome together, never partial output.
func (i *Inference) Finish(ctx context.Context, response *ai.Response, callErr error, results ...Event) error {
	if i == nil || i.owner == nil {
		return fmt.Errorf("invalid inference handle")
	}
	if response == nil && callErr == nil {
		return fmt.Errorf("inference requires response or error")
	}
	if callErr == nil && response != nil {
		callErr = response.Err
	}
	return i.owner.write(ctx, func(p *Projection) ([]Event, error) {
		if !p.data.UnfinishedInferences[i.ref.Seq] {
			return nil, fmt.Errorf("inference is not unfinished")
		}
		duration := time.Since(i.started).Milliseconds()
		finish := InferenceFinishPayload{Outcome: OutcomeSuccess, DurationMS: &duration}
		if response != nil {
			usage := response.Usage
			finish.Usage = &usage
			finish.StopReason = string(response.StopReason)
		}
		if callErr != nil {
			finish.Outcome = OutcomeFailure
			if errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) {
				finish.Outcome = OutcomeCancelled
			}
			reason := boundedReason(callErr.Error())
			if reason == "" {
				reason = "inference failed"
			}
			finish.Error = &EventError{Message: reason, Retryable: ai.IsRetryable(callErr)}
			finish.Reason = reason
		}
		batch := make([]Event, 0, len(results)+1)
		for _, result := range results {
			if result.Source != nil && *result.Source != i.ref {
				return nil, fmt.Errorf("conflicting inference result source")
			}
			if result.Payload == nil || (reflect.ValueOf(result.Payload).Kind() == reflect.Pointer && reflect.ValueOf(result.Payload).IsNil()) {
				return nil, fmt.Errorf("nil inference result")
			}
			kind := result.Payload.eventType()
			if callErr != nil && (kind == EventMessageAppend || kind == EventContextCompact) {
				return nil, fmt.Errorf("failed inference cannot publish conversation output")
			}
			switch v := result.Payload.(type) {
			case ContextCompactPayload:
				if v.Through != 0 && v.Through != i.through {
					return nil, fmt.Errorf("compaction through differs from captured prefix")
				}
				v.Through = i.through
				result.Payload = v
			case *ContextCompactPayload:
				if v == nil {
					return nil, fmt.Errorf("nil compaction")
				}
				copy := *v
				if copy.Through != 0 && copy.Through != i.through {
					return nil, fmt.Errorf("compaction through differs from captured prefix")
				}
				copy.Through = i.through
				result.Payload = copy
			}
			ref := i.ref
			result.Source = &ref
			batch = append(batch, result)
		}
		ref := i.ref
		batch = append(batch, Event{Source: &ref, Payload: finish})
		return batch, nil
	})
}

func boundedReason(s string) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= maxReason {
		return s
	}
	s = s[:maxReason]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// InferenceRequest rebuilds only the admitted prefix, independent of later edits.
func (s *Session) InferenceRequest(ctx context.Context, seq uint64) (*ai.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writer.Err(); err != nil {
		return nil, err
	}
	if err := s.store.checkID(ctx, s.ID()); err != nil {
		return nil, err
	}
	snapshot := s.Snapshot()
	event := snapshot.event(seq)
	if event == nil || event.Type != EventInferenceStart {
		return nil, fmt.Errorf("not an inference start")
	}
	start := event.Payload.(*InferenceStartPayload)
	var r *ai.Request
	if start.Input.Conversation != nil {
		s.writer.mu.Lock()
		validation := s.writer.state.validation
		s.writer.mu.Unlock()
		prefix, err := ReplayEventBatches(s.ID(), [][]Event{snapshot.data.Events[:seq-1]}, validation)
		if err != nil {
			return nil, err
		}
		r = conversationRequest(prefix, start.Input.Conversation.Append)
	} else {
		blob, err := s.requests().Request(start.Input.Standalone)
		if err != nil {
			return nil, err
		}
		r = &ai.Request{System: blob.System, Messages: blob.Messages, Tools: blob.Tools}
	}
	if g := start.Request.GenerationOptions; g != nil {
		gcopy := eventCopy(*g)
		if gcopy.MaxTokens != nil {
			r.MaxTokens = *gcopy.MaxTokens
		}
		r.Temperature = gcopy.Temperature
		r.Effort = gcopy.Effort
		r.StopSequences = gcopy.StopSequences
		r.ToolChoice = gcopy.ToolChoice
		r.CacheRetention = gcopy.CacheRetention
		r.Schema = gcopy.Schema
		r.SamplingParams = gcopy.SamplingParams
	}
	return r, nil
}
