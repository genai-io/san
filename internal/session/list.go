package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Visibility says which side of the trash a listing reads.
type Visibility string

const (
	VisibleLive  Visibility = "live"
	VisibleTrash Visibility = "trash"
	VisibleAll   Visibility = "all"
)

// ArchiveFilter says what to do with archived sessions: hide them (the
// default), show them with the rest, or show only them.
type ArchiveFilter string

const (
	ArchivedHide    ArchiveFilter = ""
	ArchivedInclude ArchiveFilter = "include"
	ArchivedOnly    ArchiveFilter = "only"
)

const (
	recentWindow     = 30 * 24 * time.Hour
	defaultListLimit = 50
	maxListLimit     = 200
)

// ListQuery selects sessions of this project. The zero value lists live,
// unarchived sessions active in the last 30 days, newest first.
type ListQuery struct {
	Workspace  string // exact workspace; empty = every workspace of the project
	Parent     string // only children of this session
	Archived   ArchiveFilter
	All        bool // include sessions older than the recent window
	Visibility Visibility
	Limit      int
	Cursor     string
	Now        time.Time // zero = time.Now(); injectable for tests
}

// ListItem is one row. Err is set instead of dropping a session whose events
// cannot be read, so damage is visible rather than hidden.
type ListItem struct {
	Overview
	Attrs   Attrs
	Trashed *Trashed
	Err     error
}

// DisplayTitle is the title a person chose, else the generated one.
func (i ListItem) DisplayTitle() string {
	if i.Attrs.Title != "" {
		return i.Attrs.Title
	}
	return i.Title
}

type ListPage struct {
	Items []ListItem
	Next  string // empty on the last page
}

type listCursor struct {
	LastActive int64  `json:"last_active"`
	ID         string `json:"id"`
}

// listOverviews is a live keyset listing ordered by (last activity, ID) descending,
// not a snapshot across requests. Pinned sessions bypass the recent window and
// the archive filter's age cut, but not an explicit archive filter; trashed
// sessions are listed without an age cut.
func (s *Store) listOverviews(ctx context.Context, q ListQuery) (ListPage, error) {
	if err := ctx.Err(); err != nil {
		return ListPage{}, err
	}
	limit := q.Limit
	switch {
	case limit == 0:
		limit = defaultListLimit
	case limit < 0 || limit > maxListLimit:
		return ListPage{}, fmt.Errorf("list limit must be between 1 and %d", maxListLimit)
	}
	vis := q.Visibility
	if vis == "" {
		vis = VisibleLive
	}
	if vis != VisibleLive && vis != VisibleTrash && vis != VisibleAll {
		return ListPage{}, fmt.Errorf("unknown visibility %q", vis)
	}
	if q.Archived != ArchivedHide && q.Archived != ArchivedInclude && q.Archived != ArchivedOnly {
		return ListPage{}, fmt.Errorf("unknown archive filter %q", q.Archived)
	}
	now := q.Now
	if now.IsZero() {
		now = time.Now()
	}
	var after *listCursor
	if q.Cursor != "" {
		c, err := decodeListCursor(q.Cursor)
		if err != nil {
			return ListPage{}, err
		}
		after = &c
	}
	lib := library{root: s.projectDir}
	var items []ListItem
	if vis != VisibleTrash {
		live, err := s.collect(ctx, s.sessionsRoot(), func(string) *Trashed { return nil })
		if err != nil {
			return ListPage{}, err
		}
		items = append(items, live...)
	}
	if vis != VisibleLive {
		trashed, err := lib.trashed(ctx)
		if err != nil {
			return ListPage{}, err
		}
		byID := map[string]Trashed{}
		for _, t := range trashed {
			byID[t.ID] = t
		}
		gone, err := s.collect(ctx, lib.trashRoot(), func(id string) *Trashed {
			t := byID[id]
			return &t
		})
		if err != nil {
			return ListPage{}, err
		}
		items = append(items, gone...)
	}
	kept := items[:0]
	for _, it := range items {
		if s.listed(it, q, now) {
			kept = append(kept, it)
		}
	}
	items = kept
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastActive != items[j].LastActive {
			return items[i].LastActive > items[j].LastActive
		}
		return items[i].SessionID > items[j].SessionID
	})
	if after != nil {
		start := sort.Search(len(items), func(i int) bool {
			a := items[i]
			return a.LastActive < after.LastActive || (a.LastActive == after.LastActive && a.SessionID < after.ID)
		})
		items = items[start:]
	}
	page := ListPage{}
	if len(items) > limit {
		last := items[limit-1]
		page.Next = encodeListCursor(listCursor{LastActive: last.LastActive, ID: last.SessionID})
		items = items[:limit]
	}
	page.Items = items
	return page, nil
}

// collect reads every session directory under root. Names starting with "." are
// staging. Unreadable sessions come back with Err set.
func (s *Store) collect(ctx context.Context, root string, trashed func(id string) *Trashed) ([]ListItem, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ListItem
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		id, dir := e.Name(), filepath.Join(root, e.Name())
		t := trashed(id)
		item := ListItem{Trashed: t}
		item.Overview, item.Err = s.overviewAt(ctx, dir, id, t == nil)
		if item.Err != nil {
			item.SessionID = id
		}
		if attrs, err := readAttrs(dir); err == nil {
			item.Attrs = attrs
		} else if item.Err == nil {
			item.Err = err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Store) listed(it ListItem, q ListQuery, now time.Time) bool {
	if it.Err != nil {
		return q.Workspace == "" && q.Parent == "" // damaged: cannot be matched, so show unfiltered
	}
	if q.Workspace != "" && it.Workspace != q.Workspace {
		return false
	}
	if q.Parent != "" && it.Parent != q.Parent {
		return false
	}
	archived := it.Attrs.ArchivedAt > 0
	switch q.Archived {
	case ArchivedHide:
		if archived {
			return false
		}
	case ArchivedOnly:
		if !archived {
			return false
		}
	}
	if it.Trashed == nil && !q.All && !it.Attrs.Pinned {
		if time.UnixMilli(it.LastActive).Before(now.Add(-recentWindow)) {
			return false
		}
	}
	return true
}

func encodeListCursor(c listCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeListCursor(s string) (listCursor, error) {
	var c listCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(s) > 512 || strictEventJSON(b, &c) != nil || c.ID == "" {
		return c, errors.New("invalid list cursor")
	}
	return c, nil
}
