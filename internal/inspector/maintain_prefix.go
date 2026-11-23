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

// Compare admitted requests, not the current context or the storage representation
// (a standalone can be either a prefixed request or a legacy unprefixed one).
func compareMaintainPrefix(events []session.Event, id string, seq uint64, maintain *ai.Request, load func(uint64) (*ai.Request, error)) prefixComparison {
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
	// Maintain appends one review message. Compare everything before it to
	// the main request plus its recorded assistant output. A later main start
	// (or finish after Maintain admission) cannot hide the completed candidate.
	n := len(maintain.Messages) - 1
	failed := false
	for i := len(completed) - 1; i >= 0; i-- {
		source := completed[i]
		route := starts[source].Request
		if route.Provider == nil || route.Model == nil || *route.Provider != *start.Request.Provider || *route.Model != *start.Request.Model {
			continue
		}
		status = "mismatch"
		output := outputs[source]
		if n <= 0 || maintain.Messages[n].Role != ai.RoleUser || len(output) > n || !sameMessages(maintain.Messages[n-len(output):n], output) {
			continue
		}
		main, err := load(source)
		if err != nil {
			failed = true
			continue
		}
		if maintain.System == main.System && ((len(maintain.Tools) == 0 && len(main.Tools) == 0) || sameNativeJSON(maintain.Tools, main.Tools)) &&
			sameMessages(maintain.Messages[:n-len(output)], main.Messages) {
			return prefixComparison{Status: "match", Source: source, Count: n}
		}
	}
	if failed {
		return unavailable
	}
	return prefixComparison{Status: status}
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
