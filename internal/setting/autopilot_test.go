package setting

import (
	"encoding/json"
	"testing"
)

// The autopilot config block loads under the user-facing "autoPilot" JSON key;
// the Go field stays AutoReview (the mechanism is a review of each gray-zone
// call). The pre-rename "autoReview" key is not accepted — the feature shipped
// unreleased, so no backward compatibility is needed.
func TestAutoPilotSettingsKey(t *testing.T) {
	var d Data
	if err := json.Unmarshal([]byte(`{"autoPilot":{"model":"anthropic/x","answerBashPrompts":true}}`), &d); err != nil {
		t.Fatalf("unmarshal autoPilot: %v", err)
	}
	if d.AutoReview.Model != "anthropic/x" {
		t.Errorf("AutoReview.Model = %q, want %q", d.AutoReview.Model, "anthropic/x")
	}
	if !d.AutoReview.AnswerBashPrompts {
		t.Error("AutoReview.AnswerBashPrompts = false, want true")
	}

	var old Data
	if err := json.Unmarshal([]byte(`{"autoReview":{"model":"x"}}`), &old); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if old.AutoReview.Model != "" {
		t.Errorf("legacy autoReview key should be ignored, got Model=%q", old.AutoReview.Model)
	}
}
