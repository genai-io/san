package agent

import (
	"context"
	"errors"

	"github.com/genai-io/san/internal/agent/reminder"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/setting"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

// RunOnce answers one message with no session around it: build the agent, hand
// it the message, run one exchange, and report what the model said.
//
// It is the direct path — Append then ThinkAct, the same one a subagent takes —
// because print mode has no inbox to wait on and nothing to interrupt. onText
// receives the answer as it streams, so a caller can print it live.
//
// Permission is the caller's: a run with nobody at the keyboard cannot prompt,
// so BuildParams.PermissionRules decides alone.
func RunOnce(ctx context.Context, p BuildParams, message core.Message, onText func(string)) (result *core.Result, err error) {
	if err := p.prepareRecording(); err != nil {
		return nil, err
	}
	messageText := message.Text()
	recallCtx, cancelRecall := context.WithCancel(ctx)
	defer cancelRecall()
	recall := p.newFastRecall(recallCtx)
	prepared := PreparedMemory{Message: message}
	if p.AutoMemory.Recall.Mode == setting.MemoryRecallWait {
		prepared, err = p.MemoryRecaller().Prepare(ctx, p.AutoMemory.Store, message, nil)
	}
	defer func() { err = errors.Join(err, prepared.Completion.Close()) }()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The project's own instructions, the way the interactive path reads them.
	// A run without them answers differently in the same repo, which is the
	// one thing a headless run must not do.
	message = withInstructions(p.CWD, prepared.Message)

	if onText != nil {
		caller := p.OnEvent
		p.OnEvent = func(ev core.Event) {
			if u, ok := ev.(sdkagent.InferenceUpdate); ok {
				if text := u.Text(); text != "" {
					onText(text)
				}
			}
			if caller != nil {
				caller(ev)
			}
		}
	}
	// Direct execution uses callbacks and has no outbox consumer.
	ag, gate, err := buildAgent(p, withoutOutbox(), withRequestContext(recall.requestContext()))
	if err != nil {
		return nil, err
	}
	if gate != nil {
		defer gate.Close()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if prepared.Completion != nil {
		if message.ID == "" {
			message.ID = core.NewMessageID()
		}
		if err := prepared.Completion.AcceptMessage(ctx, message); err != nil {
			return nil, &llm.RecordingError{Err: err}
		}
	}
	if message.ID == "" {
		message.ID = core.NewMessageID()
	}
	if p.recorder != nil {
		p.recorder.NoteUserInput(message.ID)
	}
	recall.queueQuery(message.ID, messageText)
	ag.Append(message)
	return ag.ThinkAct(ctx)
}

// withInstructions attaches the AGENTS.md chain to the message that opens a
// headless run, as a reminder rather than a prompt section — the same shape
// and the same scopes the interactive path uses, so what the model is told
// does not depend on which one asked.
func withInstructions(cwd string, msg core.Message) core.Message {
	msg.Content = reminder.AppendBlocks(msg.Content, instructionReminders(cwd)...)
	return msg
}
