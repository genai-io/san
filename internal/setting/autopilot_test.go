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
	if err := json.Unmarshal([]byte(`{"autoPilot":{"model":"anthropic/x","steers":{"bashPrompt":true}}}`), &d); err != nil {
		t.Fatalf("unmarshal autoPilot: %v", err)
	}
	if d.AutoReview.Model != "anthropic/x" {
		t.Errorf("AutoReview.Model = %q, want %q", d.AutoReview.Model, "anthropic/x")
	}
	if !d.AutoReview.Steers.BashPrompt {
		t.Error("AutoReview.Steers.BashPrompt = false, want true")
	}

	var old Data
	if err := json.Unmarshal([]byte(`{"autoReview":{"model":"x"}}`), &old); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if old.AutoReview.Model != "" {
		t.Errorf("legacy autoReview key should be ignored, got Model=%q", old.AutoReview.Model)
	}
}

// The permission steer defaults on (autopilot's baseline) and only an explicit
// false turns it off; steers survive a Clone and a same-level merge (the
// regression that made the whole autoPilot block read back as zero).
func TestAutoPilotSteersRoundTrip(t *testing.T) {
	if !(SteerSettings{}).PermissionOn() {
		t.Error("unset permission steer should default on")
	}
	off := false
	if (SteerSettings{Permission: &off}).PermissionOn() {
		t.Error("explicit permission:false should read as off")
	}

	var d Data
	if err := json.Unmarshal([]byte(`{"autoPilot":{"mission":"ship it","steers":{"turnEnd":true,"permission":false}}}`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	clone := d.Clone()
	if clone.AutoReview.Mission != "ship it" {
		t.Errorf("clone dropped mission: %q", clone.AutoReview.Mission)
	}
	if !clone.AutoReview.Steers.TurnEnd {
		t.Error("clone dropped turnEnd steer")
	}
	if clone.AutoReview.Steers.PermissionOn() {
		t.Error("clone dropped explicit permission:false")
	}
	// Deep copy: mutating the clone's pointer must not touch the original.
	on := true
	clone.AutoReview.Steers.Permission = &on
	if d.AutoReview.Steers.PermissionOn() {
		t.Error("Clone shares the permission pointer; mutation leaked to original")
	}

	merged := mergeSettings(&d, NewData())
	if merged.AutoReview.Mission != "ship it" || !merged.AutoReview.Steers.TurnEnd {
		t.Error("merge dropped the autoPilot block")
	}
}

func TestAutoReviewCloneAndIsZero(t *testing.T) {
	if !(AutoReviewSettings{}).IsZero() {
		t.Error("empty config should be zero")
	}
	on := true
	cfg := AutoReviewSettings{Mission: "x", Steers: SteerSettings{Permission: &on}}
	if cfg.IsZero() {
		t.Error("populated config should not be zero")
	}
	// A bare permission:false is still a real (non-zero) config.
	off := false
	if (AutoReviewSettings{Steers: SteerSettings{Permission: &off}}).IsZero() {
		t.Error("explicit permission:false should not be zero")
	}

	clone := cfg.Clone()
	*clone.Steers.Permission = false
	if !cfg.Steers.PermissionOn() {
		t.Error("Clone shares the permission pointer; mutation leaked")
	}
}
