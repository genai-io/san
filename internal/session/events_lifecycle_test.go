package session

import (
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/transcript"
)

// Regression: Store.Create must write session.started before returning, so
// that subsequent telemetry writes from SetObserver replay don't beat it to
// the disk. If session.started were written lazily (or only by Store.Save),
// the transcript file would already exist when Save calls Start — Start
// would short-circuit and provider/model metadata would be lost.
func TestSession_WritesSessionStartedBeforeTelemetry(t *testing.T) {
	dir := t.TempDir()
	fs, err := transcript.NewStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// Construct the writer — should write session.started synchronously.
	rec := mustSession(t, fs, CreateOptions{
		ID:        "sess",
		AgentID:   "main",
		Provider:  "anthropic",
		Model:     "claude-x",
		MaxTokens: 4096,
		Cwd:       "/tmp/project",
	})

	// Fire a SystemChange right after — mirroring what SetObserver replay
	// does inside core.NewAgent.
	rec.RecordEvent(core.SystemChange{
		Name: "identity", Slot: 0, Content: "You are X", Caller: "system:init",
	})

	records := readAllRecords(t, dir, "sess")
	if len(records) == 0 {
		t.Fatal("no records")
	}
	firstType := records[0].Type
	var foundProvider, foundModel string
	if start := records[0].Start; start != nil {
		foundProvider, foundModel = start.Provider, start.Model
	}
	if firstType != "session.started" {
		t.Fatalf("first record type = %q, want session.started", firstType)
	}
	if foundProvider != "anthropic" || foundModel != "claude-x" {
		t.Fatalf("session.started provider=%q model=%q, want anthropic/claude-x", foundProvider, foundModel)
	}
}
