package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// instanceOn models a san process attached to a project's storage file.
func instanceOn(t *testing.T, path string) *Scheduler {
	t.Helper()
	s := NewScheduler()
	s.SetStoragePath(path)
	if err := s.LoadDurable(); err != nil {
		t.Fatalf("LoadDurable: %v", err)
	}
	return s
}

func durableIDsOnDisk(t *testing.T, path string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}
		}
		t.Fatalf("read storage: %v", err)
	}
	var jobs []*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		t.Fatalf("parse storage: %v", err)
	}
	ids := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		ids[j.ID] = true
	}
	return ids
}

// The storage path is per-project, not per-process, so two san windows open on
// one repo share it. LoadDurable runs once at startup, so a job created in the
// other window was simply absent from this one's view — and the next save
// erased it from disk, with neither user having touched it.
func TestSavePreservesAnotherInstancesJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled_tasks.json")

	windowA := instanceOn(t, path)
	windowB := instanceOn(t, path) // booted before A creates anything

	jobA, err := windowA.Create("*/10 * * * *", "check the tests", true, true)
	if err != nil {
		t.Fatalf("Create in window A: %v", err)
	}

	// Anything that changes B's durable set rewrites the file from B's view.
	jobB, err := windowB.Create("*/5 * * * *", "run the linter", true, true)
	if err != nil {
		t.Fatalf("Create in window B: %v", err)
	}

	ids := durableIDsOnDisk(t, path)
	if !ids[jobA.ID] {
		t.Error("window A's job was erased by window B's save")
	}
	if !ids[jobB.ID] {
		t.Error("window B's own job is missing from disk")
	}
}

// Preserving another instance's jobs must not resurrect ones this instance
// deliberately removed — the naive "merge with disk" would do exactly that.
func TestDeleteIsNotUndoneByThePreservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled_tasks.json")

	window := instanceOn(t, path)
	job, err := window.Create("*/10 * * * *", "check the tests", true, true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ids := durableIDsOnDisk(t, path); !ids[job.ID] {
		t.Fatal("the job was never written")
	}

	if err := window.Delete(job.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if ids := durableIDsOnDisk(t, path); ids[job.ID] {
		t.Error("a deleted job came back from disk")
	}
}

// A job adopted at boot belongs to this process, so removing it is a removal
// rather than another instance's job to carry over.
func TestDeleteAfterRestartStillRemoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled_tasks.json")

	first := instanceOn(t, path)
	job, err := first.Create("*/10 * * * *", "check the tests", true, true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A fresh process picks the job up from disk, then the user deletes it.
	restarted := instanceOn(t, path)
	if !restarted.Remove(job.ID) {
		t.Fatal("the restarted process did not find the job it loaded")
	}

	if ids := durableIDsOnDisk(t, path); ids[job.ID] {
		t.Error("the job survived a delete made after a restart")
	}
}

// Session-only jobs never reach the file, whoever else is writing it.
func TestSessionJobsStayOutOfTheSharedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled_tasks.json")

	window := instanceOn(t, path)
	durable, err := window.Create("*/10 * * * *", "durable", true, true)
	if err != nil {
		t.Fatalf("Create durable: %v", err)
	}
	session, err := window.Create("*/10 * * * *", "session only", true, false)
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	ids := durableIDsOnDisk(t, path)
	if !ids[durable.ID] {
		t.Error("the durable job is missing")
	}
	if ids[session.ID] {
		t.Error("a session-only job was persisted")
	}
}
