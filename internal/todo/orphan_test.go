package todo

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/task"
)

// Adopting a store persisted by a process that is now gone must leave nothing
// claiming to be in progress: whatever was executing those tasks died with that
// process. Reconciling here rather than at each exit path is what makes the
// guarantee hold across crashes and SIGKILL, which run no cleanup code at all.
func TestSetStorageDirDemotesOrphanedTasks(t *testing.T) {
	dir := t.TempDir()

	writer := NewStore()
	if err := writer.SetStorageDir(dir); err != nil {
		t.Fatalf("SetStorageDir: %v", err)
	}
	plan := writer.Create("Refactor parser", "", "", nil)
	worker := writer.Create("Audit deps", "", "", map[string]any{metaTaskID: "bg-1"})
	for _, id := range []string{plan.ID, worker.ID} {
		if err := writer.Update(id, WithStatus(StatusInProgress)); err != nil {
			t.Fatalf("Update %s: %v", id, err)
		}
	}

	// A fresh process adopts the same directory.
	reader := NewStore()
	if err := reader.SetStorageDir(dir); err != nil {
		t.Fatalf("SetStorageDir (adopt): %v", err)
	}

	gotPlan, ok := reader.Get(plan.ID)
	if !ok {
		t.Fatalf("plan task %s missing after adopt", plan.ID)
	}
	if gotPlan.Status != StatusPending {
		t.Fatalf("plan task status = %q, want %q", gotPlan.Status, StatusPending)
	}

	gotWorker, ok := reader.Get(worker.ID)
	if !ok {
		t.Fatalf("worker task %s missing after adopt", worker.ID)
	}
	if gotWorker.Status != StatusCompleted {
		t.Fatalf("worker task status = %q, want %q", gotWorker.Status, StatusCompleted)
	}
	if detail := BackgroundStatusDetail(gotWorker); detail != StatusDetailInterrupted {
		t.Fatalf("worker status detail = %q, want %q", detail, StatusDetailInterrupted)
	}
	if !EndedAbnormally(gotWorker) {
		t.Fatal("an interrupted worker should count as having ended abnormally")
	}
}

// Adoption also happens mid-session — resuming a session re-points the store
// while this process's workers keep running. Demotion must ask the task manager
// rather than assume every caller is a fresh process, or a resume marks live
// work interrupted with no way back.
func TestSetStorageDirKeepsRunningWorker(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	running := task.NewAgentTask("live-worker", "Explore", "Audit deps", ctx, cancel)
	task.Default().RegisterTask(running)
	t.Cleanup(func() { running.Complete(nil) })

	writer := NewStore()
	if err := writer.SetStorageDir(dir); err != nil {
		t.Fatalf("SetStorageDir: %v", err)
	}
	entry := writer.Create("Audit deps", "", "", map[string]any{metaTaskID: running.ID})
	if err := writer.Update(entry.ID, WithStatus(StatusInProgress)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	adopter := NewStore()
	if err := adopter.SetStorageDir(dir); err != nil {
		t.Fatalf("SetStorageDir (adopt): %v", err)
	}

	got, ok := adopter.Get(entry.ID)
	if !ok {
		t.Fatalf("entry %s missing after adopt", entry.ID)
	}
	if got.Status != StatusInProgress {
		t.Fatalf("running worker demoted: status = %q, want %q", got.Status, StatusInProgress)
	}
}

// ReloadFromDisk exists to pick up writes from other processes mid-session, so
// it must not reconcile: an in_progress task seen there is genuinely running.
func TestReloadFromDiskPreservesExecutingTasks(t *testing.T) {
	dir := t.TempDir()

	store := NewStore()
	if err := store.SetStorageDir(dir); err != nil {
		t.Fatalf("SetStorageDir: %v", err)
	}
	live := store.Create("Run migration", "", "", nil)
	if err := store.Update(live.ID, WithStatus(StatusInProgress)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	store.ReloadFromDisk()

	got, ok := store.Get(live.ID)
	if !ok {
		t.Fatalf("task %s missing after reload", live.ID)
	}
	if got.Status != StatusInProgress {
		t.Fatalf("live task demoted by reload: status = %q, want %q", got.Status, StatusInProgress)
	}
}
