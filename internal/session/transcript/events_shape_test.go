package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestEventsShape drives a realistic session through the FileStore and then
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
	store, err := NewFileStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return now.Add(time.Duration(s) * time.Second) }

	sid := "sess-1"
	mustOK(t, "Start", store.Start(ctx, StartCommand{
		SessionID: sid, ProjectID: "proj",
		Cwd: "/repo", Provider: "anthropic", Model: "claude-sonnet-4-6",
		MaxTokens: 16384, AgentID: "main", Time: at(0),
	}))

	// system.section.added then removed
	mustOK(t, "SystemAdd", store.AppendSystemSection(ctx, AppendSystemSectionCommand{
		SessionID: sid, Time: at(1), Type: SystemSectionAdded,
		Record: SystemSectionRecord{Name: "identity", Slot: 0, Content: "You are precise.", Caller: "system:init"},
	}))
	mustOK(t, "SystemRemove", store.AppendSystemSection(ctx, AppendSystemSectionCommand{
		SessionID: sid, Time: at(2), Type: SystemSectionRemoved,
		Record: SystemSectionRecord{Name: "identity", Caller: "command:/identity"},
	}))

	// tool.added then removed
	mustOK(t, "ToolsAdd", store.AppendTool(ctx, AppendToolCommand{
		SessionID: sid, Time: at(3), Type: ToolAdded,
		Record: ToolRecord{Schema: &ToolSchemaView{
			Name: "Read", Description: "read a file",
			Parameters: json.RawMessage(`{"type":"object"}`),
		}, Caller: "tools:init"},
	}))
	mustOK(t, "ToolsRemove", store.AppendTool(ctx, AppendToolCommand{
		SessionID: sid, Time: at(4), Type: ToolRemoved,
		Record: ToolRecord{Name: "Read", Caller: "mode:plan"},
	}))

	// Three messages: first carries branch, second on same branch (should not
	// re-emit it), third switches branch (should emit).
	mustOK(t, "Msg1", store.AppendMessage(ctx, AppendMessageCommand{
		SessionID: sid, MessageID: "m1", Time: at(5),
		Role: "user", GitBranch: "main",
		Content: []ContentBlock{{Type: "text", Text: "hi"}},
	}))
	mustOK(t, "Msg2", store.AppendMessage(ctx, AppendMessageCommand{
		SessionID: sid, MessageID: "m2", ParentID: "m1", Time: at(6),
		Role: "assistant", GitBranch: "main",
		Content: []ContentBlock{{Type: "text", Text: "hello"}},
	}))
	mustOK(t, "Msg3", store.AppendMessage(ctx, AppendMessageCommand{
		SessionID: sid, MessageID: "m3", ParentID: "m2", Time: at(7),
		Role: "user", GitBranch: "feat/x",
		Content: []ContentBlock{{Type: "text", Text: "switch branch"}},
	}))

	// inference.requested + responded
	mustOK(t, "InferReq", store.AppendInference(ctx, AppendInferenceCommand{
		SessionID: sid, Time: at(8), Type: InferenceRequested,
		Record: InferenceRecord{
			Turn: 1, SystemDigest: "sha256:sys", ToolsDigest: "sha256:tools",
			MessageIDs: []string{"m1", "m2", "m3"},
		},
	}))
	mustOK(t, "InferResp", store.AppendInference(ctx, AppendInferenceCommand{
		SessionID: sid, Time: at(9), Type: InferenceResponded,
		Record: InferenceRecord{
			Turn: 1, StopReason: "end_turn", LatencyMs: 123,
			Usage: &InferenceUsage{InputTokens: 100, OutputTokens: 50},
		},
	}))

	// session.state.patched: full first patch, partial change, then an empty/no-op
	// patch must not write anything.
	mustOK(t, "State1", store.PatchState(ctx, PatchStateCommand{
		SessionID: sid, Time: at(10),
		Ops: StateOpsDiff(State{}, State{Title: "Fix bug", LastPrompt: "hi", Mode: "normal"}),
	}))
	mustOK(t, "State2", store.PatchState(ctx, PatchStateCommand{
		SessionID: sid, Time: at(11),
		Ops: StateOpsDiff(
			State{Title: "Fix bug", LastPrompt: "hi", Mode: "normal"},
			State{Title: "Fix bug", LastPrompt: "hi please", Mode: "normal"},
		),
	}))
	mustOK(t, "StateNoChange", store.PatchState(ctx, PatchStateCommand{
		SessionID: sid, Time: at(12),
		Ops: StateOpsDiff(
			State{Title: "Fix bug", LastPrompt: "hi please"},
			State{Title: "Fix bug", LastPrompt: "hi please"},
		),
	}))

	// compaction
	mustOK(t, "Compact", store.Compact(ctx, CompactCommand{
		SessionID: sid, Time: at(13), SummaryMessageID: "m2",
	}))

	// Read raw lines.
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
		t.Fatalf("session.started count = %d, want 1", len(starts))
	}
	st := starts[0]
	if st["cwd"] != "/repo" {
		t.Errorf("session.started cwd = %v, want /repo", st["cwd"])
	}
	if st["version"] != "1" {
		t.Errorf("session.started version = %v, want 1", st["version"])
	}
	sess, _ := st["session"].(map[string]any)
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

	// 4. session.state.patched: first record has 3 ops, second has 1 op, third must NOT exist.
	patches := byType["session.state.patched"]
	if len(patches) != 2 {
		t.Fatalf("session.state.patched count = %d, want 2 (no-op patch must be skipped)", len(patches))
	}
	ops1 := patches[0]["state"].(map[string]any)["ops"].([]any)
	if len(ops1) != 3 {
		t.Errorf("first session.state.patched ops len = %d, want 3", len(ops1))
	}
	ops2 := patches[1]["state"].(map[string]any)["ops"].([]any)
	if len(ops2) != 1 {
		t.Errorf("second session.state.patched ops len = %d, want 1 (lastPrompt only)", len(ops2))
	}
	if got := ops2[0].(map[string]any)["path"]; got != "lastPrompt" {
		t.Errorf("second session.state.patched op path = %v, want lastPrompt", got)
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
		t.Fatalf("session.compacted count = %d, want 1", len(cs))
	}
	csess := cs[0]["session"].(map[string]any)
	if csess["summaryMessageId"] != "m2" {
		t.Errorf("compact summaryMessageId = %v, want m2", csess["summaryMessageId"])
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
