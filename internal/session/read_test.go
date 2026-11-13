package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestStoreReadOnlyNativeRequests(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{ID: "read-native"})
	if err := s.Append(ctx, Event{Payload: InstructionsUpdatePayload{Set: map[string]InstructionSection{
		"late": {Slot: 20, Content: "late"}, "early": {Slot: 1, Content: "early"}}, Cause: "boot"}},
		Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("old", ai.RoleUser, "old")}}}); err != nil {
		t.Fatal(err)
	}
	p := s.Snapshot()
	extra := []ai.Message{eventText("extra", ai.RoleUser, "temporary")}
	request := conversationRequest(&p, extra)
	request.MaxTokens = 30
	zero := 0.0
	request.Temperature = &zero
	request.ToolChoice = ai.ToolChoiceNamed("native")
	request.SamplingParams = map[string]any{"seed": json.Number("9007199254740993")}
	call, err := s.StartInference(ctx, InferenceOptions{Purpose: PurposeCompaction, Provider: "test", Model: ai.Model{ID: "m"}, Request: request, Append: extra})
	if err != nil {
		t.Fatal(err)
	}
	prefixSeq := call.Ref().Seq
	if err = s.Append(ctx, Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("after", ai.RoleUser, "after")}}}); err != nil {
		t.Fatal(err)
	}
	if err = call.Finish(ctx, &ai.Response{}, nil, Event{Payload: ContextCompactPayload{Summary: eventText("summary", ai.RoleUser, "summary")}}); err != nil {
		t.Fatal(err)
	}
	standalone := &ai.Request{System: "outside", Messages: []ai.Message{eventText("private", ai.RoleUser, "private")}, Tools: []ai.Schema{{Name: "native", Definition: json.RawMessage(`{"const":9007199254740993}`)}}, MaxTokens: 23, ToolChoice: ai.ToolChoiceNamed("native")}
	second, err := s.StartInference(ctx, InferenceOptions{Purpose: PurposeJudge, Provider: "test", Model: ai.Model{ID: "judge"}, Request: standalone})
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Finish(ctx, &ai.Response{}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, Event{Payload: InstructionsUpdatePayload{Set: map[string]InstructionSection{"early": {Content: "changed", Slot: 1}}, Cause: "user:edit"}}); err != nil {
		t.Fatal(err)
	}
	type disk struct {
		bytes string
		mtime int64
		mode  os.FileMode
	}
	snapshot := func() map[string]disk {
		t.Helper()
		files := map[string]disk{}
		if err := filepath.Walk(store.projectDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = disk{string(data), info.ModTime().UnixNano(), info.Mode()}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot()
	for _, test := range []struct {
		seq  uint64
		want *ai.Request
	}{{prefixSeq, request}, {second.Ref().Seq, standalone}} {
		got, err := store.InferenceRequest(ctx, s.ID(), test.seq)
		if err != nil {
			t.Fatal(err)
		}
		if !sameJSON(got, test.want) {
			t.Fatalf("request changed: got %#v want %#v", got, test.want)
		}
	}
	historical, err := store.ContextRequest(ctx, s.ID(), prefixSeq)
	if err != nil {
		t.Fatal(err)
	}
	if historical.System != "early\n\nlate" || len(historical.Messages) != 1 || historical.Messages[0].ID != "old" {
		t.Fatalf("historical context: %+v", historical)
	}
	latest, err := store.ContextRequest(ctx, s.ID(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if latest.System != "changed\n\nlate" || len(latest.Messages) != 2 || latest.Messages[0].ID != "summary" || latest.Messages[1].ID != "after" {
		t.Fatalf("latest active context: %+v", latest)
	}
	projected, err := store.Read(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range projected.Messages() {
		if m.ID == "old" {
			found = true
		}
		if m.ID == "extra" || m.ID == "private" {
			t.Fatal("ephemeral input invented as history")
		}
	}
	if !found {
		t.Fatal("full history lost")
	}
	projected.data.Events[0].Time = 0
	again, err := store.Read(ctx, s.ID())
	if err != nil || again.Events()[0].Time == 0 {
		t.Fatal("read aliases stored state")
	}
	var batches int
	for _, err := range store.CommittedBatches(ctx, s.ID()) {
		if err != nil {
			t.Fatal(err)
		}
		batches++
	}
	if batches < 2 {
		t.Fatal("missing batches")
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("read modified files")
	}
	if _, err = store.InferenceRequest(ctx, s.ID(), 1); err == nil {
		t.Fatal("non-inference accepted")
	}
	if _, err = store.ContextRequest(ctx, s.ID(), 9999); err == nil {
		t.Fatal("future sequence accepted")
	}
}

func TestStoreReadAPIGuards(t *testing.T) {
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{ID: "guards"})
	read := func(ctx context.Context, id string) []error {
		_, a := store.Read(ctx, id)
		_, b := store.ContextRequest(ctx, id, 0)
		_, c := store.InferenceRequest(ctx, id, 1)
		var d error
		for _, err := range store.CommittedBatches(ctx, id) {
			d = err
			break
		}
		_, e := store.Attrs(ctx, id)
		return []error{a, b, c, d, e}
	}
	for _, err := range read(context.Background(), "../outside") {
		if err == nil {
			t.Fatal("invalid storage ID accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range read(ctx, s.ID()) {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, err := range read(context.Background(), s.ID()) {
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("closed store: %v", err)
		}
	}
}
