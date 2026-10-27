package transcript

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestSummaryAndStreamingReplayMatchFullProjection(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(t.TempDir(), "project")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	if err := store.Start(ctx, StartCommand{SessionID: "s", Time: now, Provider: "initial", Model: "first"}); err != nil {
		t.Fatal(err)
	}
	user := ai.Message{ID: "u", Role: ai.RoleUser, Content: ai.TextContent(strings.Repeat("你好", 300))}
	result := ai.Message{ID: "r", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", Content: ai.TextContent("never a prompt")})}}
	replacement := ai.Message{ID: "replacement", Role: ai.RoleUser, Content: ai.TextContent("replacement prompt")}
	for i, record := range []Record{
		{Type: MessageAppended, Message: &user},
		{Type: MessageAppended, Message: &result},
		{Type: SessionStatePatched, State: &StatePatch{Title: new("Go"), LastPrompt: new("preview")}},
		{Type: InferenceRequested, Inference: &InferenceRecord{Provider: "updated", Model: "second", Sequence: 3, Exchange: 2}},
		{Type: ConversationReplaced, Messages: []ai.Message{replacement}},
		{Type: SessionCompacted, Compaction: &Compaction{MessageID: replacement.ID}},
		{Type: SessionStatePatched, State: &StatePatch{Title: new(""), LastPrompt: new("")}},
	} {
		record.SessionID, record.Time, record.GitBranch = "s", now.Add(time.Duration(i+1)*time.Second), "branch"
		if err := store.Append(ctx, record); err != nil {
			t.Fatal(err)
		}
		records, err := store.loadRecords(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		want, err := Project(records)
		if err != nil {
			t.Fatal(err)
		}
		got, err := store.Load(ctx, "s")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("streaming projection differs at step %d: %v", i, err)
		}
		wantHistory, err := project(records, false)
		if err != nil {
			t.Fatal(err)
		}
		gotHistory, err := store.LoadHistory(ctx, "s")
		if err != nil || !reflect.DeepEqual(gotHistory, wantHistory) {
			t.Fatalf("streaming history differs at step %d: %v", i, err)
		}
		branch := ""
		for _, r := range records {
			if r.GitBranch != "" {
				branch = r.GitBranch
			}
		}
		summary, err := store.buildSummary(ctx, "s", true)
		if err != nil {
			t.Fatal(err)
		}
		if summary.Title != preview(coalesce(want.State.Title, firstUserText(want.Messages))) || summary.LastPrompt != preview(coalesce(want.State.LastPrompt, lastUserText(want.Messages))) || summary.MessageCount != len(want.Messages) || summary.Provider != want.Provider || summary.Model != want.Model || summary.CreatedAt != want.CreatedAt || summary.UpdatedAt != want.UpdatedAt || summary.GitBranch != branch {
			t.Fatalf("summary differs at step %d", i)
		}
	}
	leaf, inference, exchange, err := store.Position(ctx, "s")
	if err != nil || leaf != "replacement" || inference != 3 || exchange != 2 {
		t.Fatalf("writer position not seeded: %q %d %d %v", leaf, inference, exchange, err)
	}
}

func TestListSkipsSidechainBodyWithoutCachingPartialSummary(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(t.TempDir(), "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Start(ctx, StartCommand{SessionID: "child", Time: time.Now(), ParentSessionID: "parent"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(store.transcriptPath("child"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("{broken}\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	summary, err := store.summaryLocked(ctx, "child", false)
	if err != nil || !summary.IsSidechain {
		t.Fatalf("hidden child's body was replayed: %v", err)
	}
	if _, err := os.Stat(store.summaryPath("child")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial summary was persisted")
	}
	if _, err := store.summaryLocked(ctx, "child", true); err == nil {
		t.Fatal("partial summary hid corruption from a full listing")
	}
}

func TestSummaryRecoveryHonoursCancellation(t *testing.T) {
	store, err := NewStore(t.TempDir(), "project")
	if err != nil {
		t.Fatal(err)
	}
	startSession(t, store, "s")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.buildSummary(ctx, "s", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("summary recovery ignored cancellation: %v", err)
	}
}
