package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOtherWindowDoesNotResurrectDeletedJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled_tasks.json")
	a := instanceOn(t, path)
	deleted, err := a.Create("*/10 * * * *", "first", true, true)
	if err != nil {
		t.Fatal(err)
	}
	b := instanceOn(t, path)
	if err := a.Delete(deleted.ID); err != nil {
		t.Fatal(err)
	}
	created, err := b.Create("*/5 * * * *", "second", true, true)
	if err != nil {
		t.Fatal(err)
	}
	ids := durableIDsOnDisk(t, path)
	if ids[deleted.ID] || !ids[created.ID] || len(ids) != 1 {
		t.Fatalf("durable jobs after delete and stale save = %v", ids)
	}
	if len(b.List()) != 1 {
		t.Fatal("deleted job remains in the stale window after saving")
	}
}

func TestConcurrentWindowsKeepAllNewJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled_tasks.json")
	a, b := instanceOn(t, path), instanceOn(t, path)
	var wg sync.WaitGroup
	for _, scheduler := range []*Scheduler{a, b} {
		wg.Go(func() {
			for range 10 {
				if _, err := scheduler.Create("*/5 * * * *", "new", true, true); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if ids := durableIDsOnDisk(t, path); len(ids) != 20 {
		t.Fatalf("jobs = %d, want 20", len(ids))
	}
}

func TestUnrelatedSaveKeepsOtherWindowsSchedule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled_tasks.json")
	a := instanceOn(t, path)
	job, err := a.Create("*/10 * * * *", "first", true, true)
	if err != nil {
		t.Fatal(err)
	}
	b := instanceOn(t, path)
	a.mu.Lock()
	a.jobs[job.ID].FiredCount = 7
	a.durableChanges[job.ID] = true
	err = a.saveDurableLocked()
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Create("*/5 * * * *", "second", true, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var jobs []*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		t.Fatal(err)
	}
	for _, got := range jobs {
		if got.ID == job.ID && got.FiredCount == 7 {
			return
		}
	}
	t.Fatal("other window's job was lost or its schedule overwritten")
}

func TestSaveRefusesCorruptDurableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled_tasks.json")
	s := instanceOn(t, path)
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("*/5 * * * *", "new", true, true); err == nil {
		t.Fatal("save hid parse failure")
	}
	if !s.Empty() {
		t.Fatal("failed creation left a runnable job")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "{broken" {
		t.Fatalf("damaged file overwritten: %q, %v", got, err)
	}
}

func TestJobDeletionRetriesAfterPersistenceFailure(t *testing.T) {
	for _, operation := range []string{"Delete", "Tick"} {
		t.Run(operation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scheduled_tasks.json")
			s := instanceOn(t, path)
			job, err := s.Create("*/5 * * * *", "new", false, true)
			if err != nil {
				t.Fatal(err)
			}
			good, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
				t.Fatal(err)
			}
			if operation == "Delete" {
				if err := s.Delete(job.ID); err == nil || s.Empty() {
					t.Fatal("failed deletion must return an error and retain the job")
				}
			} else {
				job.NextFire = time.Now().Add(-time.Minute)
				if fired := s.Tick(); len(fired) != 1 || !s.Empty() {
					t.Fatal("one-shot job must fire once despite persistence failure")
				}
			}
			if err := os.WriteFile(path, good, 0o600); err != nil {
				t.Fatal(err)
			}
			if operation == "Delete" {
				if err := s.Delete(job.ID); err != nil {
					t.Fatal(err)
				}
			} else if fired := s.Tick(); len(fired) != 0 {
				t.Fatal("persistence retry fired the one-shot job twice")
			}
			if ids := durableIDsOnDisk(t, path); len(ids) != 0 {
				t.Fatalf("retry left jobs: %v", ids)
			}
		})
	}
}

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
