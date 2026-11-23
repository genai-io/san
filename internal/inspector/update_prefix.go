package inspector

import (
	"bytes"
	"encoding/json"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type prefixComparison struct {
	Status string `json:"status"`
	Source uint64 `json:"source,omitempty"`
	Count  int    `json:"count,omitempty"`
}

// Compare an admitted Update request with the admitted main request and its
// recorded native assistant output, excluding the Update-only suffix.
func compareUpdatePrefix(events []session.Event, id string, seq uint64, update *ai.Request, load func(uint64) (*ai.Request, error)) prefixComparison {
	unavailable := prefixComparison{Status: "unavailable"}
	if seq == 0 || seq > uint64(len(events)) {
		return unavailable
	}
	start, ok := events[seq-1].Payload.(*session.InferenceStartPayload)
	if !ok || start.Purpose != session.PurposeMemoryUpdate || start.Request.Provider == nil || start.Request.Model == nil {
		return unavailable
	}
	starts := make(map[uint64]*session.InferenceStartPayload)
	outputs := make(map[uint64][]ai.Message)
	var completed []uint64
	for _, event := range events[:seq-1] {
		if p, ok := event.Payload.(*session.InferenceStartPayload); ok && p.Purpose == session.PurposeTurn {
			starts[event.Seq] = p
		}
		if event.Source == nil || (event.Source.Session != "" && event.Source.Session != id) || starts[event.Source.Seq] == nil {
			continue
		}
		source := event.Source.Seq
		switch p := event.Payload.(type) {
		case *session.MessageAppendPayload:
			for _, message := range p.Messages {
				if message.Role == ai.RoleAssistant {
					outputs[source] = append(outputs[source], message)
				}
			}
		case *session.InferenceFinishPayload:
			if p.Outcome == session.OutcomeSuccess && p.StopReason == string(ai.StopEndTurn) {
				completed = append(completed, source)
			}
		}
	}
	status := "no-completed-turn"
	if len(completed) > 0 {
		status = "route-mismatch"
	}
	// A later main start (or finish after Update admission) cannot hide the
	// completed candidate.
	failed := false
	for i := len(completed) - 1; i >= 0; i-- {
		source := completed[i]
		route := starts[source].Request
		if route.Provider == nil || route.Model == nil || *route.Provider != *start.Request.Provider || *route.Model != *start.Request.Model {
			continue
		}
		status = "mismatch"
		output := outputs[source]
		messages := update.Messages
		if len(messages) <= len(output) || messages[len(messages)-1].Role != ai.RoleUser {
			continue
		}
		main, err := load(source)
		if err != nil {
			failed = true
			continue
		}
		mainLen := len(main.Messages)
		n := mainLen + len(output)
		if n == 0 || n >= len(messages) || !validUpdateSuffix(messages[n:]) {
			continue
		}
		if update.System == main.System && ((len(update.Tools) == 0 && len(main.Tools) == 0) || sameNativeJSON(update.Tools, main.Tools)) &&
			sameMessages(messages[:mainLen], main.Messages) && sameMessages(messages[mainLen:n], output) {
			return prefixComparison{Status: "match", Source: source, Count: n}
		}
	}
	if failed {
		return unavailable
	}
	return prefixComparison{Status: status}
}

// The task is followed by zero or more assistant JSON reads and user replies.
// Only validate roles here; the Inspector does not interpret Update payloads.
func validUpdateSuffix(messages []ai.Message) bool {
	if len(messages)%2 == 0 {
		return false
	}
	for i, message := range messages {
		want := ai.RoleUser
		if i%2 != 0 {
			want = ai.RoleAssistant
		}
		if message.Role != want {
			return false
		}
	}
	return true
}

func sameMessages(a, b []ai.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		// SDK message IDs are local metadata, not part of the model input.
		x.ID, y.ID = "", ""
		if !sameNativeJSON(x, y) {
			return false
		}
	}
	return true
}

func sameNativeJSON(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}
