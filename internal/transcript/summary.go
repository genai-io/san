package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/session"
)

type summaryCache struct {
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Item       Summary   `json:"summary"`
	headerOnly bool
}

func (s *Store) summaryPath(id string) string { return s.transcriptPath(id) + ".meta" }

func (s *Store) summaryLocked(ctx context.Context, id string, includeSidechain bool) (Summary, error) {
	info, err := os.Stat(s.transcriptPath(id))
	if err != nil {
		return Summary{}, err
	}
	if s.summaries == nil {
		s.summaries = make(map[string]summaryCache)
	}
	c, ok := s.summaries[id]
	if !ok {
		data, _ := os.ReadFile(s.summaryPath(id))
		_ = json.Unmarshal(data, &c)
	}
	if c.Item.ID == id && c.Size == info.Size() && c.ModifiedAt.Equal(info.ModTime()) && (!c.headerOnly || !includeSidechain) {
		s.summaries[id] = c
		return c.Item, nil
	}
	item, err := s.buildSummary(ctx, id, includeSidechain)
	if err != nil {
		return Summary{}, err
	}
	if item.IsSidechain && !includeSidechain {
		s.summaries[id] = summaryCache{Size: info.Size(), ModifiedAt: info.ModTime(), Item: item, headerOnly: true}
		return item, nil // Do not persist a partial summary.
	}
	item.Title = preview(item.Title)
	item.LastPrompt = preview(item.LastPrompt)
	c = summaryCache{Size: info.Size(), ModifiedAt: info.ModTime(), Item: item}
	data, err := json.Marshal(c)
	if err != nil {
		return Summary{}, err
	}
	// A failed cache write never makes an intact transcript inaccessible.
	_ = atomicfile.Write(s.summaryPath(id), data, 0600)
	s.summaries[id] = c
	return item, nil
}

const previewRunes = 200

func preview(s string) string {
	n := 0
	for i := range s {
		if n == previewRunes {
			return strings.Clone(s[:i])
		}
		n++
	}
	return s
}

// Decode San's listing fields only. Audit payloads and tool output never need
// to be retained or projected to show a session in the picker.
func (s *Store) buildSummary(ctx context.Context, id string, includeSidechain bool) (Summary, error) {
	var branch string
	var sidechain bool
	records := func(yield func(Record, error) bool) {
		first := true
		var messages []ai.Message
		for r, err := range s.log.Records(ctx, id) {
			if err == nil {
				err = ctx.Err()
			}
			var rec Record
			if err == nil {
				rec, err = summaryRecord(r, &messages)
			}
			if err == nil && first && (rec.Type != SessionStarted || rec.Version != SchemaVersion) {
				err = fmt.Errorf("unsupported transcript version %q (want %s)", rec.Version, SchemaVersion)
			}
			first = false
			if rec.GitBranch != "" {
				branch = rec.GitBranch
			}
			if rec.Start != nil {
				sidechain = sidechain || rec.Start.Sidechain || rec.Start.ParentSessionID != ""
			}
			if !yield(rec, err) || err != nil || sidechain && !includeSidechain {
				return
			}
		}
	}
	tx, err := projectRecords(records, true)
	if err != nil {
		return Summary{}, err
	}
	return Summary{
		ID: id, FullPath: s.transcriptPath(id), CreatedAt: tx.CreatedAt, UpdatedAt: tx.UpdatedAt,
		Title:        coalesce(tx.State.Title, firstUserText(tx.Messages)),
		LastPrompt:   coalesce(tx.State.LastPrompt, lastUserText(tx.Messages)),
		MessageCount: len(tx.Messages), GitBranch: branch, IsSidechain: sidechain,
		Provider: tx.Provider, Model: tx.Model,
	}, nil
}

func summaryRecord(r session.Record, messages *[]ai.Message) (Record, error) {
	if r.Type != session.TypeApp {
		if r.Message != nil {
			// Keep IDs and bounded user text so truncations still replay accurately.
			message := ai.Message{ID: r.Message.ID, Role: r.Message.Role}
			if message.Role == ai.RoleUser {
				message.Content = ai.TextContent(preview(firstTextBlock(r.Message.Content)))
			}
			r.Message = &message
		}
		if r.Type != session.TypeMessage && r.Type != session.TypeTruncate {
			return Record{}, fmt.Errorf("unknown session record type %q", r.Type)
		}
		return fromLog(r, messages)
	}
	var fields struct {
		SessionID string        `json:"sessionId"`
		Version   string        `json:"version"`
		Time      time.Time     `json:"time"`
		GitBranch string        `json:"gitBranch"`
		Start     *SessionStart `json:"start"`
		Fork      *SessionFork  `json:"fork"`
		State     *struct {
			Title      *string `json:"title"`
			LastPrompt *string `json:"lastPrompt"`
		} `json:"state"`
		Inference *struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"inference"`
	}
	if err := json.Unmarshal(r.Data, &fields); err != nil {
		return Record{}, err
	}
	rec := Record{Type: r.Name, SessionID: fields.SessionID, Time: fields.Time, Version: fields.Version,
		GitBranch: fields.GitBranch, Start: fields.Start, Fork: fields.Fork}
	if fields.State != nil {
		rec.State = &StatePatch{Title: fields.State.Title, LastPrompt: fields.State.LastPrompt}
		if rec.State.Title != nil {
			rec.State.Title = new(preview(*rec.State.Title))
		}
		if rec.State.LastPrompt != nil {
			rec.State.LastPrompt = new(preview(*rec.State.LastPrompt))
		}
	}
	if fields.Inference != nil {
		rec.Inference = &InferenceRecord{Provider: fields.Inference.Provider, Model: fields.Inference.Model}
	}
	return rec, nil
}
