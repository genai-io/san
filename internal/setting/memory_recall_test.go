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

func TestMemoryRecallScopes(t *testing.T) {
	trustHome(t)
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	cwd := t.TempDir()
	if err := SaveAutoMemory(false, ScopeUser); err != nil {
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
