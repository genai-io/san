package setting

import (
	"encoding/json"
	"testing"
)

// The autopilot config block loads under the user-facing "autoPilot" JSON key;
// the Go field stays AutoPilot (the mechanism is a review of each gray-zone
// call). The pre-rename "autoReview" key is not accepted — the feature shipped
// unreleased, so no backward compatibility is needed.
func TestAutoPilotSettingsKey(t *testing.T) {
	var d Data
	if err := json.Unmarshal([]byte(`{"autoPilot":{"model":"anthropic/x","steers":{"bashPrompt":true}}}`), &d); err != nil {
		t.Fatalf("unmarshal autoPilot: %v", err)
	}
	if d.AutoPilot.Model != "anthropic/x" {
		t.Errorf("AutoPilot.Model = %q, want %q", d.AutoPilot.Model, "anthropic/x")
	}
	if !d.AutoPilot.Steers.BashPrompt {
		t.Error("AutoPilot.Steers.BashPrompt = false, want true")
	}

	var old Data
	if err := json.Unmarshal([]byte(`{"autoReview":{"model":"x"}}`), &old); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if old.AutoPilot.Model != "" {
		t.Errorf("legacy autoReview key should be ignored, got Model=%q", old.AutoPilot.Model)
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
	if err := json.Unmarshal([]byte(`{"autoPilot":{"mission":"ship it","steers":{"suggest":true,"turnEnd":true,"permission":false}}}`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	clone := d.Clone()
	if clone.AutoPilot.Mission != "ship it" {
		t.Errorf("clone dropped mission: %q", clone.AutoPilot.Mission)
	}
	if !clone.AutoPilot.Steers.Suggest {
		t.Error("clone dropped suggest steer")
	}
	if !clone.AutoPilot.Steers.TurnEnd {
		t.Error("clone dropped turnEnd steer")
	}
	if clone.AutoPilot.Steers.PermissionOn() {
		t.Error("clone dropped explicit permission:false")
	}
	// Deep copy: mutating the clone's pointer must not touch the original.
	on := true
	clone.AutoPilot.Steers.Permission = &on
	if d.AutoPilot.Steers.PermissionOn() {
		t.Error("Clone shares the permission pointer; mutation leaked to original")
	}

	merged := mergeSettings(&d, NewData())
	if merged.AutoPilot.Mission != "ship it" || !merged.AutoPilot.Steers.TurnEnd || !merged.AutoPilot.Steers.Suggest {
		t.Error("merge dropped the autoPilot block")
	}
}

func TestAutoPilotCloneAndIsZero(t *testing.T) {
	if !(AutoPilotSettings{}).IsZero() {
		t.Error("empty config should be zero")
	}
	on := true
	cfg := AutoPilotSettings{Mission: "x", Steers: SteerSettings{Permission: &on}}
	if cfg.IsZero() {
		t.Error("populated config should not be zero")
	}
	// A bare permission:false is still a real (non-zero) config.
	off := false
	if (AutoPilotSettings{Steers: SteerSettings{Permission: &off}}).IsZero() {
		t.Error("explicit permission:false should not be zero")
	}

	clone := cfg.Clone()
	*clone.Steers.Permission = false
	if !cfg.Steers.PermissionOn() {
		t.Error("Clone shares the permission pointer; mutation leaked")
	}
}
