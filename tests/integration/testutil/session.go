package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/transcript"
)

// ImportSession seeds test history through the same typed Append interface as callers.
func ImportSession(store *session.Store, history *session.History) (err error) {
	if history == nil {
		return fmt.Errorf("history is nil")
	}
	meta := &history.Metadata
	if meta.ID == "" {
		meta.ID = session.NewSessionID()
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now()
	}
	if meta.Title == "" {
		meta.Title = session.GenerateTitle(history.Messages)
	}
	if meta.LastPrompt == "" {
		meta.LastPrompt = session.ExtractLastUserText(history.Messages)
	}
	opened, _, err := store.Open(meta.ID)
	if errors.Is(err, os.ErrNotExist) {
		opened, err = store.Create(session.CreateOptions{ID: meta.ID, CreatedAt: meta.CreatedAt, Cwd: meta.Cwd, Provider: meta.Provider, Model: meta.Model, ParentSessionID: meta.ParentSessionID})
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, opened.Close()) }()
	for i := range history.Messages {
		if history.Messages[i].ID == "" {
			history.Messages[i].ID = core.NewMessageID()
		}
	}
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.ConversationReplaced, Messages: history.Messages}); err != nil {
		return err
	}
	return opened.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StateRecord{Ops: []transcript.PatchOp{
		transcript.PatchTitle(meta.Title), transcript.PatchLastPrompt(meta.LastPrompt), transcript.PatchTag(meta.Tag), transcript.PatchMode(meta.Mode),
		transcript.PatchAutoPilot(meta.AutoPilot), transcript.PatchGroup(meta.Group), transcript.PatchTasks(history.Tasks),
	}}})
}
