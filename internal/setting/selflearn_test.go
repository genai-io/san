package setting

import (
	"strings"
	"testing"
)

// ptr is a tiny convenience for the *bool fields in SelfLearnSkills tests.
func ptr(b bool) *bool { return &b }

// TestSelfLearnValidate exercises the three rejected boolean combinations and
// the maxKB invariant from notes/active/l1-background-review.md §3.1 / §4.2.
// The all-zero "feature off" baseline and several legal combinations must
// validate without error.
func TestSelfLearnValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     SelfLearnSettings
		wantErr string // empty = expect ok; otherwise substring of the error
	}{
		{
			name:    "all zero (feature off)",
			cfg:     SelfLearnSettings{},
			wantErr: "",
		},
		{
			name: "explicit defaults",
			cfg: SelfLearnSettings{
				Memory: SelfLearnMemory{Enabled: true, EveryTurns: 10, MaxKB: 25},
				Skills: SelfLearnSkills{
					Enabled:        true,
					EveryToolIters: 10,
					AllowCreate:    ptr(true),
					AllowUpdate:    ptr(true),
					AllowDelete:    ptr(true),
				},
			},
			wantErr: "",
		},
		{
			name: "freeze mode (no create, only update + delete)",
			cfg: SelfLearnSettings{
				Skills: SelfLearnSkills{
					Enabled:     true,
					AllowCreate: ptr(false),
					// update + delete defaulted true
				},
			},
			wantErr: "",
		},
		{
			name: "patch-only mode (no create, no delete)",
			cfg: SelfLearnSettings{
				Skills: SelfLearnSkills{
					Enabled:     true,
					AllowCreate: ptr(false),
					AllowDelete: ptr(false),
				},
			},
			wantErr: "",
		},
		{
			name: "rejected: create without update",
			cfg: SelfLearnSettings{
				Skills: SelfLearnSkills{
					AllowCreate: ptr(true),
					AllowUpdate: ptr(false),
				},
			},
			wantErr: "allowCreate=true requires allowUpdate=true",
		},
		{
			name: "rejected: create + update without delete",
			cfg: SelfLearnSettings{
				Skills: SelfLearnSkills{
					AllowCreate: ptr(true),
					AllowUpdate: ptr(true),
					AllowDelete: ptr(false),
				},
			},
			wantErr: "allowCreate=true requires allowDelete=true",
		},
		{
			name: "rejected: advanced opt-in without update base",
			cfg: SelfLearnSettings{
				Skills: SelfLearnSkills{
					AllowUpdate:            ptr(false),
					AllowCreate:            ptr(false),
					AllowUpdateUserCreated: true,
				},
			},
			wantErr: "allowUpdateUserCreated=true requires allowUpdate=true",
		},
		{
			name: "rejected: maxKB above injection cap",
			cfg: SelfLearnSettings{
				Memory: SelfLearnMemory{MaxKB: 26},
			},
			wantErr: "out of range",
		},
		{
			name: "rejected: maxKB negative",
			cfg: SelfLearnSettings{
				Memory: SelfLearnMemory{MaxKB: -5},
			},
			wantErr: "out of range",
		},
		{
			name: "ok: maxKB at lower-than-default",
			cfg: SelfLearnSettings{
				Memory: SelfLearnMemory{MaxKB: 10},
			},
			wantErr: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected ok, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error mismatch: got %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestSelfLearnAllowAccessors confirms the *bool helpers default to true on
// nil and pass through the explicit value otherwise — the contract every
// downstream consumer (skill.go, prompts.go) depends on.
func TestSelfLearnAllowAccessors(t *testing.T) {
	zero := SelfLearnSkills{}
	if !zero.AllowCreateOr() || !zero.AllowUpdateOr() || !zero.AllowDeleteOr() {
		t.Fatal("unset (nil) booleans must default to true")
	}
	explicit := SelfLearnSkills{
		AllowCreate: ptr(false),
		AllowUpdate: ptr(false),
		AllowDelete: ptr(false),
	}
	if explicit.AllowCreateOr() || explicit.AllowUpdateOr() || explicit.AllowDeleteOr() {
		t.Fatal("explicit false must pass through")
	}
}

// TestSelfLearnMemoryMaxKBOr confirms the default fallback for MaxKB when
// unset (zero) and identity for non-zero values.
func TestSelfLearnMemoryMaxKBOr(t *testing.T) {
	if got := (SelfLearnMemory{}).MaxKBOr(); got != SelfLearnDefaultMemoryKB {
		t.Fatalf("default MaxKB: got %d, want %d", got, SelfLearnDefaultMemoryKB)
	}
	if got := (SelfLearnMemory{MaxKB: 10}).MaxKBOr(); got != 10 {
		t.Fatalf("explicit MaxKB: got %d, want 10", got)
	}
}
