package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

const (
	gcGrace         = 24 * time.Hour
	defaultCleanMax = 100
	maxCleanLimit   = 1000
	skipPinned      = "pinned"
	skipBusy        = "open elsewhere"
	skipUnfinished  = "has unfinished work"
	skipUnreadable  = "unreadable"
	skipChanged     = "changed since it was chosen"
	skipOutOfScope  = "outside the authorised workspace"
)

// CleanOptions bounds one cleanup. Retention is off unless InactiveAfter is set:
// nothing ages out of the live set by default. Expired trash is always purged.
type CleanOptions struct {
	DryRun        bool
	IDs           []string      // narrow to these sessions; never widens
	Limit         int           // max sessions moved or purged; default 100, max 1000
	Workspace     string        // retention acts only on this workspace; empty = the whole project
	InactiveAfter time.Duration // move live sessions idle this long to the trash; 0 = off
	Now           time.Time     // zero = time.Now()
}

type CleanSkip struct{ ID, Reason string }

// CleanResult says exactly what happened, or with DryRun what would. A session is
// never listed as acted on and skipped at once.
type CleanResult struct {
	Trashed   []string    // retention moved these to the trash
	Purged    []string    // expired trash removed
	Removed   []string    // garbage files removed, relative to the project
	Skipped   []CleanSkip // sessions retention left alone, with the reason
	Truncated bool        // Limit stopped the run early
}

// clean applies retention, purges expired trash and collects garbage, in that
// order, each bounded and each safe to repeat. A guard re-checks every session
// under its writer claim before it moves, so a session that was used since it
// was chosen is skipped, never moved.
func (s *Store) clean(ctx context.Context, opts CleanOptions) (CleanResult, error) {
	var res CleanResult
	limit := opts.Limit
	switch {
	case limit == 0:
		limit = defaultCleanMax
	case limit < 0 || limit > maxCleanLimit:
		return res, fmt.Errorf("clean limit must be between 1 and %d", maxCleanLimit)
	}
	if opts.InactiveAfter < 0 {
		return res, errors.New("negative retention age")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	only := map[string]bool{}
	for _, id := range opts.IDs {
		only[id] = true
	}
	lib := library{root: s.projectDir}

	if opts.InactiveAfter > 0 {
		cutoff := now.Add(-opts.InactiveAfter).UnixMilli()
		live, err := s.collect(ctx, s.sessionsRoot(), func(string) *Trashed { return nil })
		if err != nil {
			return res, err
		}
		sort.Slice(live, func(i, j int) bool { return live[i].LastActive < live[j].LastActive }) // oldest first
		for _, it := range live {
			if len(opts.IDs) > 0 && !only[it.SessionID] {
				continue
			}
			// Not in scope or not idle long enough is not a decision worth reporting.
			if it.LastActive > cutoff || (opts.Workspace != "" && it.Workspace != opts.Workspace && it.Err == nil) {
				continue
			}
			if reason := s.retentionBlock(ctx, it); reason != "" {
				res.Skipped = append(res.Skipped, CleanSkip{it.SessionID, reason})
				continue
			}
			if len(res.Trashed) >= limit {
				res.Truncated = true
				break
			}
			if !opts.DryRun {
				guard := func(dir string) error {
					state, err := s.readEventsIn(ctx, dir, it.SessionID, nil)
					if err != nil {
						return errors.New(skipUnreadable)
					}
					ov := overviewOf(it.SessionID, state.projection)
					if ov.LastActive > cutoff {
						return errors.New(skipChanged)
					}
					if opts.Workspace != "" && ov.Workspace != opts.Workspace {
						return errors.New(skipOutOfScope)
					}
					if u := state.projection.Unfinished(); len(u.Inferences)+len(u.NeverRan)+len(u.Unknown) > 0 {
						return errors.New(skipUnfinished)
					}
					return nil
				}
				if err := lib.trashChecked(ctx, it.SessionID, now, guard); err != nil {
					res.Skipped = append(res.Skipped, CleanSkip{it.SessionID, trashSkipReason(err)})
					continue
				}
			}
			res.Trashed = append(res.Trashed, it.SessionID)
		}
	}

	purged, truncated, err := lib.purge(ctx, now, opts.DryRun, only, limit-len(res.Trashed))
	res.Purged = purged
	res.Truncated = res.Truncated || truncated
	if err != nil {
		return res, err
	}
	if len(opts.IDs) == 0 {
		if res.Removed, err = s.collectGarbage(ctx, now, opts.DryRun); err != nil {
			return res, err
		}
	}
	return res, nil
}

// retentionBlock says why an idle session must not age out, or "" if it may.
func (s *Store) retentionBlock(ctx context.Context, it ListItem) string {
	switch {
	case it.Err != nil:
		return skipUnreadable
	case it.Attrs.Pinned:
		return skipPinned
	}
	state, err := s.readEvents(ctx, it.SessionID, nil)
	if err != nil {
		return skipUnreadable
	}
	if u := state.projection.Unfinished(); len(u.Inferences)+len(u.NeverRan)+len(u.Unknown) > 0 {
		return skipUnfinished
	}
	return ""
}

func trashSkipReason(err error) string {
	switch {
	case errors.Is(err, sdksession.ErrBusy):
		return skipBusy
	case errors.Is(err, ErrPinned):
		return skipPinned
	case err.Error() == skipChanged:
		return skipChanged
	}
	return err.Error()
}

// collectGarbage removes what nothing can reach: staging directories and request
// copies no event references, and cache files of sessions that no longer exist,
// each only once older than the grace period so a write in progress is never
// raced. A file it cannot attribute is kept.
func (s *Store) collectGarbage(ctx context.Context, now time.Time, dryRun bool) ([]string, error) {
	var removed []string
	remove := func(path string) error {
		rel, err := filepath.Rel(s.projectDir, path)
		if err != nil {
			rel = path
		}
		removed = append(removed, rel)
		if dryRun {
			return nil
		}
		return os.RemoveAll(path)
	}
	old := func(path string) bool {
		info, err := os.Lstat(path)
		return err == nil && now.Sub(info.ModTime()) >= gcGrace
	}
	entries, err := os.ReadDir(s.sessionsRoot())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	live := map[string]bool{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		path := filepath.Join(s.sessionsRoot(), e.Name())
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			if old(path) {
				if err := remove(path); err != nil {
					return removed, err
				}
			}
			continue
		}
		live[e.Name()] = true
		if err := s.collectBlobs(ctx, path, e.Name(), old, remove); err != nil {
			return removed, err
		}
	}
	cached, err := os.ReadDir(filepath.Join(s.projectDir, "cache"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return removed, err
	}
	for _, e := range cached {
		id := strings.TrimSuffix(e.Name(), ".json")
		path := filepath.Join(s.projectDir, "cache", e.Name())
		if id != e.Name() && !live[id] && old(path) {
			if err := remove(path); err != nil {
				return removed, err
			}
		}
	}
	return removed, nil
}

// collectBlobs removes request copies in one session's blobs/ that none of its
// events name. The short session lock covers both the read and removals, excluding
// Append's blob validation and commit. Unreadable sessions are left alone.
func (s *Store) collectBlobs(ctx context.Context, dir, id string, old func(string) bool, remove func(string) error) error {
	return (library{root: s.projectDir}).withLock(ctx, id, func() error {
		return s.collectBlobsLocked(ctx, dir, id, old, remove)
	})
}

func (s *Store) collectBlobsLocked(ctx context.Context, dir, id string, old func(string) bool, remove func(string) error) error {
	blobs := filepath.Join(dir, "blobs")
	files, err := os.ReadDir(blobs)
	if errors.Is(err, os.ErrNotExist) || len(files) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	state, err := s.readEventsIn(ctx, dir, id, nil)
	if err != nil {
		return nil
	}
	used := map[string]bool{}
	for _, e := range state.projection.data.Events {
		if in, ok := e.Payload.(*InferenceStartPayload); ok && in.Input.Standalone != "" {
			used[in.Input.Standalone] = true
		}
	}
	for _, f := range files {
		path := filepath.Join(blobs, f.Name())
		if validBlobDigest(f.Name()) && !used[f.Name()] && old(path) {
			if err := remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}
