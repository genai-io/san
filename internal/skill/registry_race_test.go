package skill

import (
	"sync"
	"testing"
)

// Initialize runs on the bubbletea goroutine — reloadProjectServices reaches
// it whenever the agent changes directory, which the agent triggers itself by
// running `cd` in a Bash call. The agent goroutine keeps running through that:
// the Skill tool and subagent prompt assembly both call Default() mid-turn.
// Before registryMu the pointer swap raced those reads.
// Run with -race; without the fix this reports DATA RACE.
func TestDefaultRegistrySurvivesConcurrentReinitialize(t *testing.T) {
	dir := t.TempDir()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() { // bubbletea goroutine: the agent ran `cd`
		defer wg.Done()
		for range 300 {
			Initialize(Options{CWD: dir})
		}
	}()
	go func() { // agent goroutine: the Skill tool resolving a name
		defer wg.Done()
		for range 300 {
			if r := DefaultIfInit(); r != nil {
				_, _ = r.Get("anything")
			}
			_ = Default().Count()
		}
	}()

	wg.Wait()
}
