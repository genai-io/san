package setting

import (
	"testing"
	"time"
)

func TestMemoryRecallDefaults(t *testing.T) {
	want := MemoryRecallConfig{Mode: MemoryRecallFast, Timeout: 5 * time.Second}
	for _, d := range []*Data{nil, {}, {MemoryRecallLookup: "unknown", MemoryRecallTimeout: "-1s"}, {MemoryRecallTimeout: "bad"}} {
		if got := d.MemoryRecall(); got != want {
			t.Errorf("Data.MemoryRecall() = %+v, want %+v", got, want)
		}
		if got := New(d).MemoryRecall(); got != want {
			t.Errorf("Settings.MemoryRecall() = %+v, want %+v", got, want)
		}
	}
}

func TestMemoryRecallOffIndependentOfMasterAndUpdate(t *testing.T) {
	trustHome(t)
	cwd := t.TempDir()
	if err := SaveAutoMemory(cwd, true, ScopeUser); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemoryUpdate(cwd, ScopeUser, true); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemoryRecallMode(cwd, ScopeUser, MemoryRecallOff); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemoryRecallMode(cwd, ScopeUser, "unknown"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	s := New(nil)
	if err := s.Reload(cwd); err != nil {
		t.Fatal(err)
	}
	if !s.AutoMemory() || !s.MemoryUpdate() || s.MemoryRecall().Mode != MemoryRecallOff {
		t.Fatalf("master = %v, Update = %v, Recall = %+v", s.AutoMemory(), s.MemoryUpdate(), s.MemoryRecall())
	}
}

func TestMemoryRecallScopes(t *testing.T) {
	trustHome(t)
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	cwd := t.TempDir()
	if err := SaveAutoMemory(cwd, false, ScopeUser); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemoryRecallMode(cwd, ScopeUser, MemoryRecallWait); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemoryRecallTimeout(cwd, ScopeUser, "2.5s"); err != nil {
		t.Fatal(err)
	}
	s := New(nil)
	check := func(mode MemoryRecallMode, timeout time.Duration) {
		t.Helper()
		if err := s.Reload(cwd); err != nil {
			t.Fatal(err)
		}
		if got, want := s.Snapshot().MemoryRecall(), (MemoryRecallConfig{mode, timeout}); got != want || s.MemoryRecall() != want {
			t.Fatalf("snapshot = %+v, live = %+v, want %+v", got, s.MemoryRecall(), want)
		}
		if s.AutoMemory() {
			t.Fatal("recall settings changed Auto Memory enablement")
		}
	}
	check(MemoryRecallWait, 2500*time.Millisecond)
	if err := SaveMemoryRecallMode(cwd, ScopeProject, MemoryRecallOff); err != nil {
		t.Fatal(err)
	}
	if mode, timeout := MemoryRecallAt(cwd, ScopeProject); mode != "off" || timeout != "" {
		t.Fatalf("project override = (%q, %q), want (off, empty)", mode, timeout)
	}
	check(MemoryRecallOff, 2500*time.Millisecond)
	if err := SaveMemoryRecallMode(cwd, ScopeProject, MemoryRecallFast); err != nil {
		t.Fatal(err)
	}
	check(MemoryRecallFast, 2500*time.Millisecond)
	if err := SaveMemoryRecallTimeout(cwd, ScopeProject, "4s"); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemoryRecallTimeout(cwd, ScopeProject, "0s"); err == nil {
		t.Fatal("zero timeout accepted")
	}
	check(MemoryRecallFast, 4*time.Second)
	if err := SaveMemoryRecallMode(cwd, ScopeProject, ""); err != nil {
		t.Fatal(err)
	}
	check(MemoryRecallWait, 4*time.Second)
	if err := NewLoader().SaveToUser(&Data{Theme: "light"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemoryRecallTimeout(cwd, ScopeProject, ""); err != nil {
		t.Fatal(err)
	}
	check(MemoryRecallWait, 2500*time.Millisecond)
}
