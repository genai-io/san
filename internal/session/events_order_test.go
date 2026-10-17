package session

import (
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

	"context"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/transcript"
)

// TestSessionWritesMessageBeforeInference confirms the "causes before
// consumers" invariant: when the agent appends a user message and immediately
// fires PreInfer, the writer must land message.appended on disk first.
//
// Before the OnAppend wiring, message.appended was written by Store.Save at
// turn end, so the on-disk order was inference.requested → message.appended,
// which made the viewer's "context at this inference" view incorrect (the
// active chain looked empty).
func TestSessionWritesMessageBeforeInference(t *testing.T) {
	dir := t.TempDir()
	fs, err := transcript.NewStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := fs.Start(context.Background(), transcript.StartCommand{
		SessionID: "sess-order", Cwd: "/tmp", Provider: "anthropic", Model: "claude-x", Time: time.Now(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rec := mustSession(t, fs, CreateOptions{ID: "sess-order", AgentID: "main",
		Provider: "anthropic", Model: "claude-x", MaxTokens: 4096, Cwd: "/tmp",
	})

	// Simulate one turn: the user message appends, then the call that references
	// it goes out.
	userMsg := core.Message{ID: "m1", Role: ai.RoleUser, Content: ai.TextContent("hello")}
	rec.RecordEvent(sdkagent.MessageAdded{Message: userMsg})

	rec.RecordEvent(sdkagent.InferenceStart{Inference: &sdkagent.Inference{
		System:   "you are san",
		Messages: []core.Message{userMsg},
	}})

	// Assistant reply appends after PostInfer.
	rec.RecordEvent(sdkagent.InferenceEnd{Response: &ai.Response{
		StopReason: ai.StopEndTurn, Usage: core.Usage{Input: 10, Output: 5},
	}})
	assistantMsg := core.Message{ID: "m2", Role: ai.RoleAssistant, Content: ai.TextContent("hi")}
	rec.RecordEvent(sdkagent.MessageAdded{Message: assistantMsg})

	records := readAllRecords(t, dir, "sess-order")

	// Find indices of key records.
	idxOf := func(predicate func(transcript.Record) bool) int {
		for i, r := range records {
			if predicate(r) {
				return i
			}
		}
		return -1
	}
	iUserMsg := idxOf(func(r transcript.Record) bool {
		return r.Type == transcript.MessageAppended && r.Message != nil && r.Message.ID == "m1"
	})
	iReq := idxOf(func(r transcript.Record) bool { return r.Type == transcript.InferenceRequested })
	iResp := idxOf(func(r transcript.Record) bool { return r.Type == transcript.InferenceResponded })
	iAssistantMsg := idxOf(func(r transcript.Record) bool {
		return r.Type == transcript.MessageAppended && r.Message != nil && r.Message.ID == "m2"
	})

	if iUserMsg < 0 || iReq < 0 || iResp < 0 || iAssistantMsg < 0 {
		t.Fatalf("missing records: userMsg=%d req=%d resp=%d assistantMsg=%d (have %d)",
			iUserMsg, iReq, iResp, iAssistantMsg, len(records))
	}

	// "Causes before consumers": user message before its inference.requested.
	if iUserMsg >= iReq {
		t.Errorf("user message at idx %d should precede inference.requested at idx %d", iUserMsg, iReq)
	}
	// Assistant message lands after the responded boundary.
	if iAssistantMsg <= iResp {
		t.Errorf("assistant message at idx %d should follow inference.responded at idx %d", iAssistantMsg, iResp)
	}
	// And of course request precedes response.
	if iReq >= iResp {
		t.Errorf("inference.requested at idx %d must precede inference.responded at idx %d", iReq, iResp)
	}
}

// When the writer writes message.appended via OnAppend, a follow-up
// Store.Save on the TUI path (OmitMessageWrites=true) must NOT add a second
// copy of the same message under a different ID. Without this guard the
// JSONL would balloon by ~2× and the active chain projection would see
// duplicate messages.
func TestSessionAndSaveDoNotDoubleWrite(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	fs := store.transcriptStore

	if err := fs.Start(context.Background(), transcript.StartCommand{
		SessionID: "sess-dup", Cwd: dir, Provider: "p", Model: "m", Time: time.Now(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	rec := mustSession(t, fs, CreateOptions{ID: "sess-dup", AgentID: "main", Cwd: dir})

	// Session writes the two messages (agent-side path).
	rec.RecordEvent(sdkagent.MessageAdded{Message: core.Message{
		ID: "agent-m1", Role: ai.RoleUser, Content: ai.TextContent("hi"),
	}})
	rec.RecordEvent(sdkagent.MessageAdded{Message: core.Message{
		ID: "agent-m2", Role: ai.RoleAssistant, Content: ai.TextContent("hello"),
	}})

	// State writes share the same session and cannot copy the conversation.
	if err := rec.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched,
		State: &transcript.StatePatch{Title: new("hello")}}); err != nil {
		t.Fatal(err)
	}

	count := 0
	for _, r := range readAllRecords(t, dir, "sess-dup") {
		if r.Type == transcript.MessageAppended {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("message.appended count = %d, want 2 (no duplicates)", count)
	}
}

// permission.required and permission.decided must share the same RequestID
// so audit consumers can join the pair without timestamp heuristics. The ID
// is supplied by the PermissionRules closure and flows through
// PermGateRequest unchanged.
func TestSessionPermissionRequiredDecidedShareRequestID(t *testing.T) {
	dir := t.TempDir()
	fs, err := transcript.NewStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := fs.Start(context.Background(), transcript.StartCommand{
		SessionID: "sess-perm", Cwd: "/tmp", Time: time.Now(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	rec := mustSession(t, fs, CreateOptions{ID: "sess-perm", AgentID: "main"})

	_ = rec.Append(context.Background(), transcript.Record{Type: transcript.PermissionRequired, Permission: &transcript.PermissionRecord{
		RequestID: "req-42", Tool: "Bash", Source: "ask", Mode: "normal",
	}})
	_ = rec.Append(context.Background(), transcript.Record{Type: transcript.PermissionDecided, Permission: &transcript.PermissionRecord{
		RequestID: "req-42", Tool: "Bash", Decision: "permit", Source: "user", Mode: "normal",
	}})

	var req, dec transcript.Record
	for _, r := range readAllRecords(t, dir, "sess-perm") {
		switch r.Type {
		case transcript.PermissionRequired:
			req = r
		case transcript.PermissionDecided:
			dec = r
		}
	}
	if req.Permission == nil || dec.Permission == nil {
		t.Fatalf("missing pair: req=%+v dec=%+v", req, dec)
	}
	if req.Permission.RequestID == "" {
		t.Fatalf("required RequestID empty")
	}
	if req.Permission.RequestID != dec.Permission.RequestID {
		t.Errorf("requestId mismatch: required=%q decided=%q",
			req.Permission.RequestID, dec.Permission.RequestID)
	}
}

// On Continue/Resume the agent reloads prior messages via SetMessages
// without firing OnAppend. If the writer doesn't seed messageLeafID from
// the existing transcript, the first new message lands with parentId=""
// and the replay's leaf walk drops the entire loaded history.
func TestSessionSeedsLastMessageIDFromExistingTranscript(t *testing.T) {
	dir := t.TempDir()
	fs, err := transcript.NewStore(dir, "proj-seed")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := fs.Start(context.Background(), transcript.StartCommand{
		SessionID: "sess-seed", Cwd: "/tmp", Time: time.Now(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Seed the file with two pre-existing messages, simulating a session
	// the user is now resuming.
	rec0 := mustSession(t, fs, CreateOptions{ID: "sess-seed", AgentID: "main"})
	rec0.RecordEvent(sdkagent.MessageAdded{Message: core.Message{
		ID: "old-m1", Role: ai.RoleUser, Content: ai.TextContent("first turn"),
	}})
	rec0.RecordEvent(sdkagent.MessageAdded{Message: core.Message{
		ID: "old-m2", Role: ai.RoleAssistant, Content: ai.TextContent("first reply"),
	}})

	rec0.Close()

	// Simulate process restart with a fresh writer; without seeding it
	// would start the conversation over at the next message.
	rec := mustSession(t, fs, CreateOptions{ID: "sess-seed", AgentID: "main"})

	rec.RecordEvent(sdkagent.MessageAdded{Message: core.Message{
		ID: "new-m3", Role: ai.RoleUser, Content: ai.TextContent("second turn"),
	}})

	rec.Close()
	tx, err := fs.Load(context.Background(), "sess-seed")
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Messages) != 3 || tx.Messages[1].ID != "old-m2" || tx.Messages[2].ID != "new-m3" {
		t.Errorf("resumed conversation = %+v, want old-m1 old-m2 new-m3 (seeding failed)", tx.Messages)
	}
}

func TestSessionPermissionRecordsCarryToolInput(t *testing.T) {
	dir := t.TempDir()
	fs, err := transcript.NewStore(dir, "proj-perm-input")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := fs.Start(context.Background(), transcript.StartCommand{
		SessionID: "sess-pi", Cwd: "/tmp", Time: time.Now(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	rec := mustSession(t, fs, CreateOptions{ID: "sess-pi", AgentID: "main"})

	_ = rec.Append(context.Background(), transcript.Record{Type: transcript.PermissionRequired, Permission: &transcript.PermissionRecord{
		RequestID: "rq-1", Tool: "Bash",
		Input:  []byte(`{"command":"git status"}`),
		Source: "ask", Mode: "normal",
	}})
	// Scope is an opaque label set by the caller (the approval modal owns
	// the vocabulary); transcript schema treats it as a pass-through string.
	_ = rec.Append(context.Background(), transcript.Record{Type: transcript.PermissionDecided, Permission: &transcript.PermissionRecord{
		RequestID: "rq-1", Tool: "Bash",
		Input:    []byte(`{"command":"git status"}`),
		Decision: transcript.PermissionPermit,
		Source:   transcript.PermissionSourceUser,
		Scope:    "session",
		Reason:   "user approved", Mode: "normal",
	}})

	var reqRec, decRec transcript.Record
	for _, r := range readAllRecords(t, dir, "sess-pi") {
		switch r.Type {
		case transcript.PermissionRequired:
			reqRec = r
		case transcript.PermissionDecided:
			decRec = r
		}
	}
	if reqRec.Permission == nil || decRec.Permission == nil {
		t.Fatalf("missing pair: req=%+v dec=%+v", reqRec, decRec)
	}
	if got := string(reqRec.Permission.Input); got != `{"command":"git status"}` {
		t.Errorf("required input on disk = %q, want %q", got, `{"command":"git status"}`)
	}
	if decRec.Permission.Scope != "session" {
		t.Errorf("decided scope = %q, want %q", decRec.Permission.Scope, "session")
	}
}

// A blank-content control signal (e.g. SigStop carrier) must not produce a
// message.appended record on the wire — those entries aren't part of the
// model-visible chain.
func TestSessionSkipsEmptyMessages(t *testing.T) {
	dir := t.TempDir()
	fs, err := transcript.NewStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := fs.Start(context.Background(), transcript.StartCommand{
		SessionID: "sess-skip", Cwd: "/tmp", Provider: "p", Model: "m", Time: time.Now(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	rec := mustSession(t, fs, CreateOptions{ID: "sess-skip", AgentID: "main"})

	rec.RecordEvent(sdkagent.MessageAdded{Message: core.Message{
		ID: "empty-1", Role: ai.RoleUser, // no content, no tool result, no images
	}})

	for _, r := range readAllRecords(t, dir, "sess-skip") {
		if r.Type == transcript.MessageAppended {
			t.Fatalf("empty message produced a record: %+v", r)
		}
	}
}
