package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileStoreStartAppendListLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}

	now := time.Date(2026, 4, 6, 16, 0, 0, 0, time.UTC)
	if err := store.Start(context.Background(), StartCommand{
		SessionID: "tx-1",
		ProjectID: "proj-1",
		Cwd:       "/tmp/project",
		Provider:  "openai",
		Model:     "gpt-test",
		Time:      now,
	}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m1",
		Time:      now.Add(time.Second),
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m2",
		ParentID:  "m1",
		Time:      now.Add(2 * time.Second),
		Role:      "assistant",
		Content:   []ContentBlock{{Type: "text", Text: "world"}},
		GitBranch: "main",
	}); err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}
	if err := store.PatchState(context.Background(), PatchStateCommand{
		SessionID: "tx-1",
		Time:      now.Add(3 * time.Second),
		Ops:       []PatchOp{PatchTitle("Fix bug"), PatchLastPrompt("hello")},
	}); err != nil {
		t.Fatalf("PatchState(): %v", err)
	}

	items, err := store.List(context.Background(), "proj-1", ListOptions{})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 list item, got %d", len(items))
	}
	if items[0].Title != "Fix bug" {
		t.Fatalf("Title = %q, want %q", items[0].Title, "Fix bug")
	}
	if items[0].LastPrompt != "hello" {
		t.Fatalf("LastPrompt = %q, want %q", items[0].LastPrompt, "hello")
	}

	transcript, err := store.Load(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if transcript.State.Title != "Fix bug" {
		t.Fatalf("projected title = %q, want %q", transcript.State.Title, "Fix bug")
	}
	if len(transcript.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(transcript.Messages))
	}
}

func TestFileStoreCompactAndFork(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}

	now := time.Date(2026, 4, 6, 16, 10, 0, 0, time.UTC)
	if err := store.Start(context.Background(), StartCommand{
		SessionID: "tx-1",
		ProjectID: "proj-1",
		Cwd:       "/tmp/project",
		Time:      now,
	}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m1",
		Time:      now.Add(time.Second),
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatalf("AppendMessage(): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m2",
		ParentID:  "m1",
		Time:      now.Add(2 * time.Second),
		Role:      "assistant",
		Content:   []ContentBlock{{Type: "text", Text: "world"}},
	}); err != nil {
		t.Fatalf("AppendMessage(m2): %v", err)
	}
	if err := store.Compact(context.Background(), CompactCommand{
		SessionID:        "tx-1",
		Time:             now.Add(3 * time.Second),
		SummaryMessageID: "m1",
	}); err != nil {
		t.Fatalf("Compact(): %v", err)
	}

	transcript, err := store.Load(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(transcript.Messages) != 2 {
		t.Fatalf("expected 2 messages after compact (boundary=m1), got %d", len(transcript.Messages))
	}

	if err := store.Fork(context.Background(), ForkCommand{
		SourceSessionID: "tx-1",
		NewSessionID:    "tx-2",
		Time:            now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("Fork(): %v", err)
	}
	forked, err := store.Load(context.Background(), "tx-2")
	if err != nil {
		t.Fatalf("Load(fork): %v", err)
	}
	if forked.ParentID != "tx-1" {
		t.Fatalf("fork ParentID = %q, want %q", forked.ParentID, "tx-1")
	}
	store.mu.RLock()
	forkRecords, err := store.loadRecordsLocked(store.transcriptPath("tx-2"))
	store.mu.RUnlock()
	if err != nil {
		t.Fatalf("load fork records: %v", err)
	}
	for _, rec := range forkRecords {
		if rec.SessionID != "tx-2" {
			t.Fatalf("fork record SessionID = %q, want tx-2", rec.SessionID)
		}
		if rec.ID != "" && !strings.HasPrefix(rec.ID, "tx-2:") {
			t.Fatalf("fork record ID = %q, want tx-2 prefix", rec.ID)
		}
		if rec.Version != SchemaVersion {
			t.Fatalf("fork record Version = %q, want %q", rec.Version, SchemaVersion)
		}
	}
}

// AppendMessage must be idempotent on the message ID and survive across
// FileStore instances (the second store reads the existing file fresh).
func TestFileStoreAppendMessageIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}

	now := time.Date(2026, 4, 6, 16, 20, 0, 0, time.UTC)
	if err := store.Start(context.Background(), StartCommand{
		SessionID: "tx-1", Cwd: "/tmp/project", Provider: "openai", Model: "gpt-test", Time: now,
	}); err != nil {
		t.Fatalf("Start(): %v", err)
	}

	msg := AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m1",
		Time:      now.Add(time.Second),
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: "hello"}},
	}
	for i := range 3 {
		if err := store.AppendMessage(context.Background(), msg); err != nil {
			t.Fatalf("AppendMessage() #%d: %v", i, err)
		}
	}

	// A fresh store (empty cache) must still dedupe via the on-disk scan.
	fresh, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore() fresh: %v", err)
	}
	if err := fresh.AppendMessage(context.Background(), msg); err != nil {
		t.Fatalf("AppendMessage() fresh: %v", err)
	}

	tx, err := store.Load(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(tx.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1 (dedup failed)", len(tx.Messages))
	}
}

func TestFileStoreWritesExpectedEventShapes(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}

	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	if err := store.Start(context.Background(), StartCommand{
		SessionID: "tx-shape",
		ProjectID: "proj-1",
		Cwd:       "/repo",
		Provider:  "anthropic",
		Model:     "claude-test",
		MaxTokens: 8192,
		AgentID:   "main",
		ParentID:  "parent-session",
		Time:      now,
	}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if err := store.AppendSystemSection(context.Background(), AppendSystemSectionCommand{
		SessionID: "tx-shape",
		Time:      now.Add(time.Second),
		Type:      SystemSectionAdded,
		Record:    SystemSectionRecord{Name: "identity", Slot: 0, Content: "You are precise.", Caller: "system:init"},
	}); err != nil {
		t.Fatalf("AppendSystemSection(add): %v", err)
	}
	if err := store.AppendSystemSection(context.Background(), AppendSystemSectionCommand{
		SessionID: "tx-shape",
		Time:      now.Add(2 * time.Second),
		Type:      SystemSectionRemoved,
		Record:    SystemSectionRecord{Name: "identity", Caller: "command:/identity"},
	}); err != nil {
		t.Fatalf("AppendSystemSection(remove): %v", err)
	}
	if err := store.AppendTool(context.Background(), AppendToolCommand{
		SessionID: "tx-shape",
		Time:      now.Add(3 * time.Second),
		Type:      ToolAdded,
		Record: ToolRecord{Schema: &ToolSchemaView{
			Name:        "Read",
			Description: "read a file",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}, Caller: "tools:init"},
	}); err != nil {
		t.Fatalf("AppendTool(add): %v", err)
	}
	if err := store.AppendTool(context.Background(), AppendToolCommand{
		SessionID: "tx-shape",
		Time:      now.Add(4 * time.Second),
		Type:      ToolRemoved,
		Record:    ToolRecord{Name: "Read", Caller: "mode:plan"},
	}); err != nil {
		t.Fatalf("AppendTool(remove): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-shape",
		MessageID: "m1",
		Time:      now.Add(5 * time.Second),
		GitBranch: "main",
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: "hello", Source: "user"}},
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID:   "tx-shape",
		MessageID:   "m2",
		ParentID:    "m1",
		Time:        now.Add(6 * time.Second),
		AgentID:     "subagent-1",
		IsSidechain: true,
		Role:        "assistant",
		Content: []ContentBlock{{
			Type:  "tool_use",
			ID:    "toolu-1",
			Name:  "Read",
			Input: json.RawMessage(`{"path":"README.md"}`),
		}},
	}); err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}
	if err := store.AppendInference(context.Background(), AppendInferenceCommand{
		SessionID: "tx-shape",
		Time:      now.Add(7 * time.Second),
		Type:      InferenceRequested,
		Record: InferenceRecord{
			Turn:         1,
			SystemDigest: "sha256:system",
			ToolsDigest:  "sha256:tools",
			MessageIDs:   []string{"m1", "m2"},
		},
	}); err != nil {
		t.Fatalf("AppendInference(request): %v", err)
	}
	if err := store.AppendInference(context.Background(), AppendInferenceCommand{
		SessionID: "tx-shape",
		Time:      now.Add(8 * time.Second),
		Type:      InferenceResponded,
		Record: InferenceRecord{
			Turn:       1,
			StopReason: "end_turn",
			LatencyMs:  1234,
			Usage:      &InferenceUsage{InputTokens: 100, OutputTokens: 20, CacheReadInputTokens: 10},
		},
	}); err != nil {
		t.Fatalf("AppendInference(response): %v", err)
	}
	if err := store.PatchState(context.Background(), PatchStateCommand{
		SessionID: "tx-shape",
		Time:      now.Add(9 * time.Second),
		Ops:       []PatchOp{PatchTitle("Trace shape"), PatchLastPrompt("hello"), PatchMode("plan")},
	}); err != nil {
		t.Fatalf("PatchState(): %v", err)
	}
	if err := store.Compact(context.Background(), CompactCommand{
		SessionID:        "tx-shape",
		Time:             now.Add(10 * time.Second),
		SummaryMessageID: "m2",
	}); err != nil {
		t.Fatalf("Compact(): %v", err)
	}

	records := readRawRecordMaps(t, dir, "tx-shape")
	if got, want := recordTypes(records), []string{
		SessionStarted,
		SystemSectionAdded,
		SystemSectionRemoved,
		ToolAdded,
		ToolRemoved,
		MessageAppended,
		MessageAppended,
		InferenceRequested,
		InferenceResponded,
		SessionStatePatched,
		SessionCompacted,
	}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("record types = %v, want %v", got, want)
	}

	start := records[0]
	assertTopLevel(t, start, "id", "sessionId", "time", "type", "cwd", "version", "session")
	assertNoTopLevel(t, start, "provider", "model", "maxTokens", "message", "system", "tool", "inference", "state")
	assertString(t, start, "cwd", "/repo")
	session := objectAt(t, start, "session")
	assertString(t, session, "provider", "anthropic")
	assertString(t, session, "model", "claude-test")
	assertNumber(t, session, "maxTokens", 8192)
	assertString(t, session, "agentId", "main")
	assertString(t, session, "parentId", "parent-session")

	sysAdd := records[1]
	assertTopLevel(t, sysAdd, "id", "sessionId", "time", "type", "system")
	system := objectAt(t, sysAdd, "system")
	assertString(t, system, "name", "identity")
	assertString(t, system, "content", "You are precise.")
	assertString(t, system, "caller", "system:init")

	sysRemove := records[2]
	system = objectAt(t, sysRemove, "system")
	assertString(t, system, "name", "identity")
	assertString(t, system, "caller", "command:/identity")
	assertNoTopLevel(t, system, "content")

	toolAdd := records[3]
	assertTopLevel(t, toolAdd, "id", "sessionId", "time", "type", "tool")
	tool := objectAt(t, toolAdd, "tool")
	schema := objectAt(t, tool, "schema")
	assertString(t, schema, "name", "Read")
	assertTopLevel(t, schema, "name", "description", "input_schema")
	assertNoTopLevel(t, schema, "parameters")

	toolRemove := records[4]
	tool = objectAt(t, toolRemove, "tool")
	assertString(t, tool, "name", "Read")
	assertString(t, tool, "caller", "mode:plan")
	assertNoTopLevel(t, tool, "schema")

	userMsg := records[5]
	assertTopLevel(t, userMsg, "id", "sessionId", "time", "type", "gitBranch", "message")
	assertString(t, userMsg, "gitBranch", "main")
	message := objectAt(t, userMsg, "message")
	assertString(t, message, "messageId", "m1")
	assertString(t, message, "role", "user")
	assertNoTopLevel(t, message, "parentId")

	assistantMsg := records[6]
	assertString(t, assistantMsg, "parentId", "m1")
	assertString(t, assistantMsg, "agentId", "subagent-1")
	if got, ok := assistantMsg["isSidechain"].(bool); !ok || !got {
		t.Fatalf("isSidechain = %#v, want true", assistantMsg["isSidechain"])
	}
	message = objectAt(t, assistantMsg, "message")
	assertString(t, message, "messageId", "m2")
	assertNoTopLevel(t, message, "parentId")

	request := records[7]
	assertTopLevel(t, request, "id", "sessionId", "time", "type", "inference")
	assertNoTopLevel(t, request, "provider", "model", "maxTokens", "message", "system", "tool", "state")
	inference := objectAt(t, request, "inference")
	assertNumber(t, inference, "turn", 1)
	assertString(t, inference, "systemDigest", "sha256:system")
	assertString(t, inference, "toolsDigest", "sha256:tools")
	assertStringSlice(t, inference, "messageIds", []string{"m1", "m2"})
	assertNoTopLevel(t, inference, "stopReason", "usage")

	response := records[8]
	inference = objectAt(t, response, "inference")
	assertNumber(t, inference, "turn", 1)
	assertString(t, inference, "stopReason", "end_turn")
	assertNumber(t, inference, "latencyMs", 1234)
	usage := objectAt(t, inference, "usage")
	assertNumber(t, usage, "inputTokens", 100)
	assertNumber(t, usage, "outputTokens", 20)
	assertNumber(t, usage, "cacheReadTokens", 10)
	assertNoTopLevel(t, inference, "systemDigest", "toolsDigest", "messageIds")

	state := objectAt(t, records[9], "state")
	ops, ok := state["ops"].([]any)
	if !ok || len(ops) != 3 {
		t.Fatalf("state.ops = %+v, want 3 patch ops", state["ops"])
	}

	compact := records[10]
	session = objectAt(t, compact, "session")
	assertString(t, session, "summaryMessageId", "m2")
	assertNoTopLevel(t, session, "provider", "model", "maxTokens", "agentId")
}

func readRawRecordMaps(t *testing.T, baseDir, sessionID string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(baseDir, "transcripts", sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	out := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d json decode: %v\n%s", i+1, err, line)
		}
		out = append(out, rec)
	}
	return out
}

func recordTypes(records []map[string]any) []string {
	out := make([]string, 0, len(records))
	for _, rec := range records {
		t, _ := rec["type"].(string)
		out = append(out, t)
	}
	return out
}

func objectAt(t *testing.T, obj map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := obj[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %+v, want object", key, obj[key])
	}
	return v
}

func assertTopLevel(t *testing.T, obj map[string]any, keys ...string) {
	t.Helper()
	if len(obj) != len(keys) {
		t.Fatalf("keys = %v, want exactly %v in object %+v", mapKeys(obj), keys, obj)
	}
	for _, key := range keys {
		if _, ok := obj[key]; !ok {
			t.Fatalf("missing key %q in object %+v", key, obj)
		}
	}
}

func assertNoTopLevel(t *testing.T, obj map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := obj[key]; ok {
			t.Fatalf("unexpected key %q in object %+v", key, obj)
		}
	}
}

func assertString(t *testing.T, obj map[string]any, key, want string) {
	t.Helper()
	got, ok := obj[key].(string)
	if !ok || got != want {
		t.Fatalf("%s = %#v, want %q", key, obj[key], want)
	}
}

func assertNumber(t *testing.T, obj map[string]any, key string, want float64) {
	t.Helper()
	got, ok := obj[key].(float64)
	if !ok || got != want {
		t.Fatalf("%s = %#v, want %v", key, obj[key], want)
	}
}

func assertStringSlice(t *testing.T, obj map[string]any, key string, want []string) {
	t.Helper()
	raw, ok := obj[key].([]any)
	if !ok || len(raw) != len(want) {
		t.Fatalf("%s = %#v, want %v", key, obj[key], want)
	}
	for i := range want {
		got, ok := raw[i].(string)
		if !ok || got != want[i] {
			t.Fatalf("%s[%d] = %#v, want %q", key, i, raw[i], want[i])
		}
	}
}

func mapKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	return keys
}

// The transcript index defers per-message writes to a flush boundary: appends
// stay authoritative in the in-process cache, while the on-disk file another
// process reads only catches up on FlushIndex. Start still writes a new
// session's entry eagerly so the picker never loses it.
func TestFileStoreIndexWritesDeferUntilFlush(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if err := store.Start(context.Background(), StartCommand{
		SessionID: "tx-1", Cwd: "/tmp/project", Provider: "openai", Model: "gpt-test", Time: now,
	}); err != nil {
		t.Fatalf("Start(): %v", err)
	}

	// Start writes eagerly: a fresh store reading only disk already sees the
	// session, with no messages counted yet.
	if got := freshIndexMessageCount(t, dir, "tx-1"); got != 0 {
		t.Fatalf("after Start, on-disk MessageCount = %d, want 0", got)
	}

	for i, id := range []string{"m1", "m2", "m3"} {
		if err := store.AppendMessage(context.Background(), AppendMessageCommand{
			SessionID: "tx-1",
			MessageID: id,
			Time:      now.Add(time.Duration(i+1) * time.Second),
			Role:      "user",
			Content:   []ContentBlock{{Type: "text", Text: id}},
		}); err != nil {
			t.Fatalf("AppendMessage(%s): %v", id, err)
		}
	}

	// In-process reads are always current — the cache holds every append.
	if got := indexMessageCount(t, store, "tx-1"); got != 3 {
		t.Fatalf("in-process MessageCount = %d, want 3", got)
	}
	// ...but those appends have not touched disk, so another process still
	// reads the pre-append count.
	if got := freshIndexMessageCount(t, dir, "tx-1"); got != 0 {
		t.Fatalf("before flush, on-disk MessageCount = %d, want 0 (writes should defer)", got)
	}

	if err := store.FlushIndex(); err != nil {
		t.Fatalf("FlushIndex(): %v", err)
	}
	if got := freshIndexMessageCount(t, dir, "tx-1"); got != 3 {
		t.Fatalf("after flush, on-disk MessageCount = %d, want 3", got)
	}
}

func indexMessageCount(t *testing.T, store *FileStore, id string) int {
	t.Helper()
	items, err := store.List(context.Background(), "proj-1", ListOptions{})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	for _, it := range items {
		if it.SessionID == id {
			return it.MessageCount
		}
	}
	t.Fatalf("session %s not found in list", id)
	return 0
}

// freshIndexMessageCount opens a brand-new FileStore over the same dir so List
// reads the on-disk index rather than any in-process cache.
func freshIndexMessageCount(t *testing.T, dir, id string) int {
	t.Helper()
	fresh, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(fresh): %v", err)
	}
	return indexMessageCount(t, fresh, id)
}

// The index is a bounded preview cache rewritten every turn: a long prompt is
// capped in the persisted Title/LastPrompt (rune-safe, so multibyte text is
// never split), and the file is compact JSON — while the transcript keeps the
// full text. This is what stops a big paste from bloating the per-turn rewrite.
func TestFileStoreIndexCapsPreviewText(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}

	// A prompt well over the cap in multibyte runes: a byte-slice cut would split
	// a character mid-rune, a rune-slice cut keeps a clean prefix.
	full := strings.Repeat("码", indexPreviewMaxRunes+50)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if err := store.Start(context.Background(), StartCommand{
		SessionID: "tx-1", Cwd: "/tmp/project", Provider: "openai", Model: "gpt-test", Time: now,
	}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m1",
		Time:      now.Add(time.Second),
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: full}},
	}); err != nil {
		t.Fatalf("AppendMessage(): %v", err)
	}
	if err := store.FlushIndex(); err != nil {
		t.Fatalf("FlushIndex(): %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, transcriptIndexFile))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	// Compact marshal ⇒ no pretty-print newlines (JSON escapes any in-string
	// newline as "\\n", so a raw 0x0A byte can only come from indentation).
	if bytes.ContainsRune(raw, '\n') {
		t.Fatalf("index contains newlines; want compact marshal")
	}
	var idx fileIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if len(idx.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(idx.Entries))
	}
	want := string([]rune(full)[:indexPreviewMaxRunes])
	if got := idx.Entries[0].Title; got != want {
		t.Fatalf("persisted Title = %d runes, want a %d-rune prefix", len([]rune(got)), indexPreviewMaxRunes)
	}
	if got := idx.Entries[0].LastPrompt; got != want {
		t.Fatalf("persisted LastPrompt = %d runes, want a %d-rune prefix", len([]rune(got)), indexPreviewMaxRunes)
	}

	// The transcript still holds the full, uncapped text — only the index preview
	// is bounded.
	tx, err := store.Load(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if got := firstTextBlock(tx.Messages[0].Content); got != full {
		t.Fatalf("transcript text was truncated: %d runes, want %d", len([]rune(got)), len([]rune(full)))
	}
}
