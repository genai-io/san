package setting

import (
	"sync"
	"testing"
)

// SessionPermissions is shared by two goroutines: the UI one rewrites the
// posture when the user cycles the operation mode (Shift+Tab), and the agent
// one reads it on every tool call. Before the mutex and Snapshot, the writes
// in env.ApplyModePermissions raced the reads in HasPermissionToUseTool — the
// race detector flagged both the WorkingDirectories append and the Mode write.
// Run with -race; without the fix this test reports DATA RACE.
func TestSessionPermissionsSurviveConcurrentPostureChange(t *testing.T) {
	data := &Data{}
	perms := NewSessionPermissions()

	var wg sync.WaitGroup
	wg.Add(2)

	// UI goroutine: cycle Normal → AutoAccept repeatedly.
	go func() {
		defer wg.Done()
		for range 2000 {
			perms.ResetPosture()
			perms.SetMode(ModeAutoAccept)
			perms.GrantEditPosture("/repo")
		}
	}()

	// Agent goroutine: one permission check per tool call.
	go func() {
		defer wg.Done()
		for range 2000 {
			data.HasPermissionToUseTool("Edit", map[string]any{"file_path": "/repo/a.go"}, perms)
		}
	}()

	wg.Wait()
}

// A decision must not straddle a posture change: Snapshot is taken once, so
// every step of the evaluation sees the same mode and allowances.
func TestSnapshotDetachesFromLaterMutation(t *testing.T) {
	perms := NewSessionPermissions()
	perms.SetMode(ModeAutoAccept)
	perms.GrantEditPosture("/repo")
	perms.AllowPattern("Bash(ls)")

	snap := perms.Snapshot()

	perms.ResetPosture()
	perms.AddWorkingDirectory("/elsewhere")
	perms.AllowPattern("Bash(rm)")

	if snap.Mode != ModeAutoAccept {
		t.Errorf("Mode = %q, want the mode at snapshot time %q", snap.Mode, ModeAutoAccept)
	}
	if !snap.AllowAllEdits {
		t.Error("AllowAllEdits = false, want the posture at snapshot time")
	}
	if len(snap.WorkingDirectories) != 1 || snap.WorkingDirectories[0] != "/repo" {
		t.Errorf("WorkingDirectories = %v, want only /repo", snap.WorkingDirectories)
	}
	if snap.AllowedPatterns["Bash(rm)"] {
		t.Error("snapshot picked up a pattern granted after it was taken")
	}
	if !snap.AllowedPatterns["Bash(ls)"] {
		t.Error("snapshot lost a pattern granted before it was taken")
	}
}

func TestSnapshotOfNilSessionIsNil(t *testing.T) {
	var perms *SessionPermissions
	if perms.Snapshot() != nil {
		t.Error("Snapshot of a nil session should stay nil; callers pass an optional session")
	}
}
