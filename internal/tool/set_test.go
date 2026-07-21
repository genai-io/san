package tool

import "testing"

// The task tracker is the main conversation's plan, and every conversation
// shares one process-global todo store. Letting a subagent call the tracker
// tools leaks its private planning into the main panel, so they must be
// parent-only — even an explicit allow list cannot opt a subagent back in.
func TestSubagentsCannotUseTrackerTools(t *testing.T) {
	trackerTools := []string{ToolTaskCreate, ToolTaskUpdate, ToolTaskList, ToolTaskGet}

	main := (&Set{}).Tools()
	for _, name := range trackerTools {
		if !schemasContain(main, name) {
			t.Errorf("main conversation should keep %s", name)
		}
	}

	agentAll := (&Set{IsAgent: true}).Tools()
	for _, name := range trackerTools {
		if schemasContain(agentAll, name) {
			t.Errorf("subagent (all tools) must not get %s", name)
		}
	}

	agentAllow := (&Set{IsAgent: true, Allow: trackerTools}).Tools()
	for _, name := range trackerTools {
		if schemasContain(agentAllow, name) {
			t.Errorf("subagent must not get %s even when it is allow-listed", name)
		}
	}
}
