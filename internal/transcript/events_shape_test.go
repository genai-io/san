package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// TestEventsShape drives a realistic session through the Store and then
// re-reads the JSONL line-by-line to assert every documented invariant from
// docs/inspector.md:
//
//   - session.started carries every session-wide constant (provider, model,
//     maxTokens, agentId, cwd, version) exactly once.
//   - No later record restamps any of those constants.
//   - gitBranch is only emitted on message.appended when it changes.
//   - inference.* records carry only turn-local fields (no provider/model/
//     maxTokens, no agentId).
//   - session.state.patched is sparse: same-state save emits no record; partial
//     change emits only the diff.
//
// The test also prints the raw JSONL so a human reader can eyeball the wire
// shape (`go test -run TestEventsShape -v`).
func TestEventsShape(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return now.Add(time.Duration(s) * time.Second) }

	sid := "sess-1"
	mustOK(t, "Start", store.Start(ctx, StartCommand{
		SessionID: sid,
		Cwd:       "/repo", Provider: "anthropic", Model: "claude-sonnet-4-6",
		MaxTokens: 16384, AgentID: "main", Time: at(0),
	}))

	// system.section.added then removed
	mustOK(t, "SystemAdd", store.Append(ctx, Record{SessionID: sid, Time: at(1), Type: SystemSectionAdded, System: &SystemSectionRecord{Name: "identity", Slot: 0, Content: "You are precise.", Caller: "system:init"}}))
	mustOK(t, "SystemRemove", store.Append(ctx, Record{SessionID: sid, Time: at(2), Type: SystemSectionRemoved, System: &SystemSectionRecord{Name: "identity", Caller: "command:/identity"}}))

	// tool.added then removed
	mustOK(t, "ToolsAdd", store.Append(ctx, Record{SessionID: sid, Time: at(3), Type: ToolAdded, Tool: &ToolRecord{Schema: &ai.Schema{
		Name: "Read", Description: "read a file",
		Definition: json.RawMessage(`{"type":"object"}`),
	}, Caller: "tools:init"},
	}))
	mustOK(t, "ToolsRemove", store.Append(ctx, Record{SessionID: sid, Time: at(4), Type: ToolRemoved, Tool: &ToolRecord{Name: "Read", Caller: "mode:plan"}}))

	// Three messages: first carries branch, second on same branch (should not
	// re-emit it), third switches branch (should emit).
	mustOK(t, "Msg1", store.Append(ctx, Record{SessionID: sid, Time: at(5), GitBranch: "main", Type: MessageAppended, Message: &ai.Message{ID: "m1", Role: "user", Content: ai.Content{ai.TextBlock("hi")}}}))
	mustOK(t, "Msg2", store.Append(ctx, Record{SessionID: sid, Time: at(6), ParentMessageID: "m1", GitBranch: "main", Type: MessageAppended, Message: &ai.Message{ID: "m2", Role: "assistant", Content: ai.Content{ai.TextBlock("hello")}}}))
	mustOK(t, "Msg3", store.Append(ctx, Record{SessionID: sid, Time: at(7), ParentMessageID: "m2", GitBranch: "feat/x", Type: MessageAppended,

		// inference.requested + responded
		Message: &ai.Message{ID: "m3", Role: "user", Content: ai.Content{ai.TextBlock("switch branch")}},
	}))

	mustOK(t, "InferReq", store.Append(ctx, Record{SessionID: sid, Time: at(8), Type: InferenceRequested, Inference: &InferenceRecord{
		Sequence: 1, SystemDigest: "sha256:sys", ToolsDigest: "sha256:tools",
		MessageIDs: []string{"m1", "m2", "m3"},
	},
	}))
	mustOK(t, "InferResp", store.Append(ctx, Record{SessionID: sid, Time: at(9), Type: InferenceResponded, Inference: &InferenceRecord{
		Sequence: 1, StopReason: "end_turn", LatencyMs: 123,
		Usage: &InferenceUsage{InputTokens: 100, OutputTokens: 50},
	},
	}))

	// session.state.patched: full first patch, partial change, then an empty/no-op
	// patch must not write anything.
	mustOK(t, "State1", store.Append(ctx, Record{SessionID: sid, Time: at(10), Type: SessionStatePatched, State: &StateRecord{Title: new("Fix bug"), LastPrompt: new("hi"), Mode: new("normal")}}))
	mustOK(t, "State2", store.Append(ctx, Record{SessionID: sid, Time: at(11), Type: SessionStatePatched, State: &StateRecord{LastPrompt: new("hi please")}}))
	mustOK(t, "StateNoChange", store.Append(ctx, Record{SessionID: sid, Time: at(12), Type: SessionStatePatched, State: &StateRecord{Title: new("Fix bug"), LastPrompt: new("hi please")}}))

	// compaction
	mustOK(t, "Compact", store.Append(ctx, Record{SessionID: sid, Time: at(13), Type: SessionCompacted, Compaction: &Compaction{

		// Read raw lines.
		MessageID: "m2"},
	}))

	raw, err := os.ReadFile(store.transcriptPath(sid))
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	// Print for human inspection.
	t.Log("\n--- transcripts/" + sid + ".jsonl ---")
	for i, ln := range lines {
		t.Logf("%2d  %s", i, ln)
	}

	// Decode into raw maps to inspect actual wire shape, not Go struct projections.
	records := make([]map[string]any, 0, len(lines))
	for i, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("line %d invalid JSON: %v\n  line: %s", i, err, ln)
		}
		records = append(records, m)
	}

	// Group by type for assertions.
	byType := map[string][]map[string]any{}
	for _, r := range records {
		t, _ := r["type"].(string)
		byType[t] = append(byType[t], r)
	}

	// 1. Exactly one session.started, carrying every constant.
	starts := byType["session.started"]
	if len(starts) != 1 {
		t.Fatalf("session.started count = %d, want 2", len(starts))
	}
	st := starts[0]
	if st["cwd"] != "/repo" {
		t.Errorf("session.started cwd = %v, want /repo", st["cwd"])
	}
	if st["version"] != SchemaVersion {
		t.Errorf("session.started version = %v, want 2", st["version"])
	}
	sess, _ := st["start"].(map[string]any)
	for k, want := range map[string]any{
		"provider": "anthropic", "model": "claude-sonnet-4-6",
		"maxTokens": float64(16384), "agentId": "main",
	} {
		if sess[k] != want {
			t.Errorf("session.started.session.%s = %v, want %v", k, sess[k], want)
		}
	}

	// 2. No record except session.started carries cwd/version/provider/model/maxTokens/agentId.
	for i, r := range records {
		ty, _ := r["type"].(string)
		if ty == "session.started" {
			continue
		}
		for _, k := range []string{"cwd", "version", "agentId"} {
			if v, has := r[k]; has && v != "" && v != nil {
				t.Errorf("line %d type=%s carries forbidden envelope field %s=%v", i, ty, k, v)
			}
		}
		// inference payload must not include provider/model/maxTokens.
		if inf, ok := r["inference"].(map[string]any); ok {
			for _, k := range []string{"provider", "model", "maxTokens"} {
				if _, has := inf[k]; has {
					t.Errorf("line %d %s.inference carries forbidden field %s", i, ty, k)
				}
			}
		}
	}

	// 3. gitBranch is sparse.
	msgs := byType["message.appended"]
	if len(msgs) != 3 {
		t.Fatalf("message.appended count = %d, want 3", len(msgs))
	}
	if msgs[0]["gitBranch"] != "main" {
		t.Errorf("msg[0] gitBranch = %v, want main", msgs[0]["gitBranch"])
	}
	if _, has := msgs[1]["gitBranch"]; has {
		t.Errorf("msg[1] should omit gitBranch (unchanged), got %v", msgs[1]["gitBranch"])
	}
	if msgs[2]["gitBranch"] != "feat/x" {
		t.Errorf("msg[2] gitBranch = %v, want feat/x", msgs[2]["gitBranch"])
	}

	// 4. session.state.patched: first record has 3 fields, second has 1 field, third must NOT exist.
	patches := byType["session.state.patched"]
	if len(patches) != 2 {
		t.Fatalf("session.state.patched count = %d, want 2 (no-op patch must be skipped)", len(patches))
	}
	state1 := patches[0]["state"].(map[string]any)
	if len(state1) != 3 {
		t.Errorf("first state fields = %v, want 3", state1)
	}
	state2 := patches[1]["state"].(map[string]any)
	if len(state2) != 1 || state2["lastPrompt"] == nil {
		t.Errorf("second state fields = %v, want lastPrompt only", state2)
	}

	// 5. inference.requested has digests + messageIds; inference.responded has
	//    stopReason/latency/usage; neither has the other's fields.
	reqs := byType["inference.requested"]
	resps := byType["inference.responded"]
	if len(reqs) != 1 || len(resps) != 1 {
		t.Fatalf("inference req/resp counts = %d/%d", len(reqs), len(resps))
	}
	req := reqs[0]["inference"].(map[string]any)
	if req["systemDigest"] == nil || req["toolsDigest"] == nil || req["messageIds"] == nil {
		t.Errorf("inference.requested missing required fields: %v", req)
	}
	for _, forbidden := range []string{"stopReason", "latencyMs", "usage"} {
		if _, has := req[forbidden]; has {
			t.Errorf("inference.requested carries forbidden field %q", forbidden)
		}
	}
	resp := resps[0]["inference"].(map[string]any)
	for _, want := range []string{"stopReason", "latencyMs", "usage"} {
		if _, has := resp[want]; !has {
			t.Errorf("inference.responded missing %q", want)
		}
	}
	for _, forbidden := range []string{"systemDigest", "toolsDigest", "messageIds"} {
		if _, has := resp[forbidden]; has {
			t.Errorf("inference.responded carries forbidden field %q", forbidden)
		}
	}

	// 6. session.compacted carries only the summaryMessageId, no constants.
	cs := byType["session.compacted"]
	if len(cs) != 1 {
		t.Fatalf("session.compacted count = %d, want 2", len(cs))
	}
	csess := cs[0]["compaction"].(map[string]any)
	if csess["messageId"] != "m2" {
		t.Errorf("compact summaryMessageId = %v, want m2", csess["messageId"])
	}
	for _, forbidden := range []string{"provider", "model", "maxTokens", "agentId"} {
		if _, has := csess[forbidden]; has {
			t.Errorf("session.compacted carries forbidden field %q", forbidden)
		}
	}

	// 7. Replay the file via Project() and confirm the projection still works
	//    end-to-end despite the sparse wire encoding.
	loaded, err := store.Load(ctx, sid)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Provider != "anthropic" || loaded.Model != "claude-sonnet-4-6" {
		t.Errorf("Project lost provider/model: %+v", loaded)
	}
	if loaded.State.Title != "Fix bug" || loaded.State.LastPrompt != "hi please" {
		t.Errorf("Project lost last-wins state: %+v", loaded.State)
	}
	// After compaction with boundary=m2, the active chain is m2..m3.
	if len(loaded.Messages) != 2 || loaded.Messages[0].ID != "m2" || loaded.Messages[1].ID != "m3" {
		t.Errorf("active chain after compact = %+v", loaded.Messages)
	}

	// 8. lastGitBranch index helper still finds the latest non-empty branch
	//    even though only two of three messages carry one on the wire.
	items, err := store.List(ctx, "proj", ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].GitBranch != "feat/x" {
		t.Errorf("List index gitBranch = %+v, want feat/x", items)
	}

	// Print a per-line size summary so the redundancy savings are visible.
	t.Log("\n--- per-record byte sizes ---")
	var total int
	for i, ln := range lines {
		total += len(ln)
		var m map[string]any
		_ = json.Unmarshal([]byte(ln), &m)
		ty, _ := m["type"].(string)
		t.Logf("%2d  %4d bytes  %s", i, len(ln), ty)
	}
	t.Logf("total: %d bytes across %d records", total, len(lines))
}

func mustOK(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	_ = fmt.Sprint // keep fmt import in case future asserts need it
}
