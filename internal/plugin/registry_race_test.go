package plugin

import (
	"sync"
	"testing"
)

// Enable/Disable write p.Enabled under r.mu, but the read surface used to hand
// out the live *Plugin, so callers dereferenced it outside the lock.
//
// Reached by: /plugin -> install from a marketplace -> Esc while the clone runs
// -> reopen /plugin. The install goroutine is still in Enable (after a git
// clone plus a settings write) while refreshInstalledPlugins reads p.Enabled on
// the UI goroutine. Run with -race; without the fix this reports DATA RACE.
func TestReadSurfaceIsSafeAgainstConcurrentEnable(t *testing.T) {
	r := NewRegistry()
	r.Register(&Plugin{Manifest: Manifest{Name: "alpha"}})
	r.Register(&Plugin{Manifest: Manifest{Name: "beta"}, Enabled: true})

	var wg sync.WaitGroup
	wg.Add(2)

	go func() { // the install goroutine
		defer wg.Done()
		for range 500 {
			r.mu.Lock()
			r.plugins["alpha"].Enabled = !r.plugins["alpha"].Enabled
			r.mu.Unlock()
		}
	}()
	go func() { // the UI goroutine refreshing the panel
		defer wg.Done()
		for range 500 {
			for _, p := range r.List() {
				_ = p.Enabled
			}
			for _, p := range r.GetEnabled() {
				_ = p.Enabled
			}
			if p, ok := r.Get("alpha"); ok {
				_ = p.Enabled
			}
			for _, p := range r.GetByScope(ScopeUser) {
				_ = p.Enabled
			}
		}
	}()

	wg.Wait()
}

// A snapshot is a copy, so mutating what a caller was handed must not reach
// back into the registry.
func TestReadSurfaceHandsOutCopies(t *testing.T) {
	r := NewRegistry()
	r.Register(&Plugin{Manifest: Manifest{Name: "alpha"}, Enabled: true})

	got, ok := r.Get("alpha")
	if !ok {
		t.Fatal("Get(alpha) not found")
	}
	got.Enabled = false

	again, _ := r.Get("alpha")
	if !again.Enabled {
		t.Error("mutating a returned plugin changed the registry's own copy")
	}
}

// SetCwd exists so a caller outside the package cannot assign the
// lock-protected field directly; NewInstaller used to.
func TestSetCwdIsSafeAgainstConcurrentLoad(t *testing.T) {
	r := NewRegistry()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 500 {
			r.SetCwd("/one")
		}
	}()
	go func() {
		defer wg.Done()
		for range 500 {
			r.SetCwd("/two")
		}
	}()
	wg.Wait()
}
