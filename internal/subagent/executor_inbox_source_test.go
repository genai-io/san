package subagent

import (
	"context"
	"iter"
	"testing"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	agenttools "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestNativeSendMessageAcceptedInputProvenance(t *testing.T) {
	ctx := context.Background()
	store, parent := continuationSessions(t, "owner")
	recipient, err := store.Create(ctx, session.CreateOptions{AgentID: "recipient"})
	if err != nil {
		t.Fatal(err)
	}
	defer recipient.Close()
	route := task.NewID()
	senderDriver := aitest.New(aitest.Asks(ai.ToolCall{ID: "interim-send", Name: tool.ToolSendMessage, Input: `{"to":"` + route + `","message":"interim report"}`}), aitest.Says("sender final answer"))
	sender := NewExecutor(continuationProvider{senderDriver}, t.TempDir(), "aitest", nil)
	sender.SetParentPermissionMode(func() PermissionMode { return PermissionBypass })
	sender.SetDisabledTools(map[string]bool{tool.ToolSendMessage: false})
	if err := sender.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	var senderResult *AgentResult
	receiverDriver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		var sendErr error
		senderResult, sendErr = sender.Run(ctx, tool.AgentExecRequest{Prompt: "send interim report", ParentToolCallID: "spawn-sender"})
		if sendErr != nil {
			t.Error(sendErr)
			return aitest.Fails(sendErr)(ctx, req)
		}
		return aitest.Says("receiver first answer")(ctx, req)
	}, aitest.Says("receiver processed report"))
	recorder := session.NewRecorder(recipient, "aitest")
	ag := core.NewAgent(core.Config{Client: func([]core.Message) (*ai.Client, error) { return receiverDriver.Client(), nil }, System: core.NewSystem(), Tools: core.NewTools(), MaxSteps: 2, OutboxBuf: -1, InferenceAdmission: recorder.InferenceAdmission, ToolAdmission: recorder.ToolAdmission, OutputAdmission: recorder.OutputAdmission, OnEvent: recorder.RecordEvent})
	ag.Append(core.UserMessage("recipient initial task", nil))
	result, err := runWithInbox(ctx, ag, route, 2, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.Err() != nil {
		t.Fatal(recorder.Err())
	}
	if result.Content != "receiver processed report" || senderResult == nil {
		t.Fatalf("exchange failed: %+v", result)
	}
	sent, err := store.Read(ctx, senderResult.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := sent.ToolCallSource("interim-send")
	if !ok || source.Session != senderResult.AgentID || source.Seq == senderResult.SourceMessageSeq {
		t.Fatalf("interim source fabricated from final answer: %+v", source)
	}
	projection := recipient.Snapshot()
	accepted := 0
	var deliveredID string
	for _, event := range projection.Events() {
		if event.Source == nil || event.Source.Session != source.Session || event.Source.Seq != source.Seq {
			continue
		}
		payload, ok := event.Payload.(*session.MessageAppendPayload)
		if !ok || len(payload.Messages) != 1 {
			t.Fatalf("invalid accepted native input: %+v", event)
		}
		accepted++
		deliveredID = payload.Messages[0].ID
	}
	if accepted != 1 || deliveredID == "" {
		t.Fatalf("accepted native source count=%d id=%q", accepted, deliveredID)
	}
	active := projection.ActiveMessages()
	actual := ag.Messages()
	if len(active) != len(actual) {
		t.Fatalf("native/SDK lengths differ: %d/%d", len(active), len(actual))
	}
	for i := range actual {
		if active[i].ID != actual[i].ID {
			t.Fatalf("accepted-input consumption order mismatch at %d: native=%q (%s), SDK=%q (%s)", i, active[i].Text(), active[i].ID, actual[i].Text(), actual[i].ID)
		}
	}
}

func TestSourcedInboxRejectsInvalidOrClosedStorageWithoutSendAck(t *testing.T) {
	for _, kind := range []string{"closed", "missing-source", "incomplete-source"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			store, source := continuationSessions(t, "source")
			message := core.UserMessage("source evidence", nil)
			message.ID = core.NewMessageID()
			if err := source.Append(ctx, session.Event{Payload: session.MessageAppendPayload{Messages: []ai.Message{message}}}); err != nil {
				t.Fatal(err)
			}
			recipient, err := store.Create(ctx, session.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer recipient.Close()
			route := task.NewID()
			d := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				seq := uint64(2)
				if kind == "closed" {
					if err := recipient.Close(); err != nil {
						t.Error(err)
					}
				}
				if kind == "missing-source" {
					seq = 999999
				}
				if kind == "incomplete-source" {
					seq = 0
				}
				callCtx := tool.WithMessageSource(tool.WithAgentID(ctx, "sender"), source.ID(), seq)
				sent := agenttools.NewSendMessageTool().Execute(callCtx, map[string]any{"to": route, "message": "must reject"}, ".")
				if sent.Success {
					t.Errorf("positive send acknowledgement despite %s: %+v", kind, sent)
				}
				return aitest.Says("done")(ctx, req)
			})
			ag := core.NewAgent(core.Config{Client: func([]core.Message) (*ai.Client, error) { return d.Client(), nil }, System: core.NewSystem(), Tools: core.NewTools(), MaxSteps: 1, OutboxBuf: -1})
			ag.Append(core.UserMessage("initial task", nil))
			if _, err := runWithInbox(ctx, ag, route, 1, recipient); err != nil {
				t.Fatal(err)
			}
			for _, m := range ag.Messages() {
				if m.Text() == "must reject" {
					t.Fatal("rejected input entered native queue")
				}
			}
			projection := recipient.Snapshot()
			if len(projection.Events()) != 1 {
				t.Fatal("invalid input changed persisted recipient")
			}
			if broker.Send(broker.Message{To: route, Content: "after close"}) {
				t.Fatal("route not closed")
			}
		})
	}
}
