package autopilot

import (
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestStopEvidence(t *testing.T) {
	for _, tc := range []struct {
		reason               core.StopReason
		resumable, explained bool
	}{
		{core.StopEndTurn, true, false}, {core.StopMaxSteps, true, true}, {core.StopTruncated, true, true}, {core.StopCanceled, false, false}, {core.StopHook, false, false}, {"future", false, false},
	} {
		t.Run(string(tc.reason), func(t *testing.T) {
			got, s := StopEvidence(tc.reason)
			if got != tc.resumable || (s != "") != tc.explained {
				t.Fatalf("StopEvidence = %v, %q", got, s)
			}
		})
	}
}

func TestParseDecision(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		want        Decision
		invalid     bool
	}{
		{"fenced instruction", "```json\n{\"continue\":true,\"instruction\":\"  run tests  \"}\n```", Decision{Continue: true, Instruction: "run tests"}, false},
		{"complete", `{"done":true}`, Decision{Done: true}, false},
		{"implicit handback", `{}`, Decision{}, false},
		{"missing instruction", `{"continue":true,"instruction":"  "}`, Decision{}, true},
		{"contradictory", `{"done":true,"continue":true,"instruction":"go"}`, Decision{}, true},
		{"done with work", `{"done":true,"instruction":"go"}`, Decision{}, true},
		{"handback with work", `{"instruction":"go"}`, Decision{}, true},
		{"wrong type", `{"continue":"true"}`, Decision{}, true},
		{"no object", "later", Decision{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDecision(tc.reply)
			if (err != nil) != tc.invalid || err == nil && got != tc.want {
				t.Fatalf("ParseDecision = %+v, %v", got, err)
			}
		})
	}
}
