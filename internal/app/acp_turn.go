package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

func (s *acpSession) prompt(ctx context.Context, turn *acp.Turn, req protocol.PromptRequest) (response protocol.PromptResponse, err error) {
	s.input.set(turn)
	defer s.input.set(nil)
	s.input.bindCancellation(ctx, func() { s.runtime.Cancel() })
	defer s.input.bindCancellation(nil, nil)
	if s.runtimeBuildParams.Plugins != nil {
		ctx = plugin.WithRegistry(ctx, s.runtimeBuildParams.Plugins)
	}
	if err := s.nativeSession.Validate(); err != nil {
		return response, err
	}
	var text strings.Builder
	for _, block := range req.Prompt {
		value, ok := block.Value.(protocol.ContentBlockTextVariant)
		if !ok {
			return response, fmt.Errorf("only text prompts are supported")
		}
		text.WriteString(value.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		return response, fmt.Errorf("empty prompt")
	}
	prompt, pluginRoot, err := s.commandPrompt(text.String())
	if err != nil {
		return response, err
	}
	s.runtime.SetPluginRoot(pluginRoot)
	ctx = plugin.WithRoot(ctx, pluginRoot)
	hookCtx, cancelHook := context.WithTimeout(ctx, 5*time.Second)
	blocked, reason := agent.CheckPrompt(hookCtx, text.String(), s.hooks, s.reminders)
	cancelHook()
	if blocked {
		return response, fmt.Errorf("prompt blocked: %s", reason)
	}
	if err := ctx.Err(); err != nil {
		return response, err
	}
	if !s.runtime.Active() {
		if err := s.configureAgents(); err != nil {
			return response, err
		}
		projection := s.nativeSession.Session.Snapshot()
		if len(projection.ActiveMessages()) > 0 {
			if _, err := s.runtime.Resume(s.runtimeBuildParams); err != nil {
				return response, err
			}
		} else if err := s.runtime.Start(s.runtimeBuildParams, nil); err != nil {
			return response, err
		}
	}
	cancellationDone := make(chan struct{})
	stopCancellation := context.AfterFunc(ctx, func() {
		defer close(cancellationDone)
		s.runtime.Cancel()
	})
	defer func() {
		if !stopCancellation() {
			<-cancellationDone
		}
	}()
	gate := s.runtime.PermissionGate()
	permissionsCtx, cancelPermissions := context.WithCancel(ctx)
	permissionsDone := make(chan struct{})
	go s.handlePermissionRequests(permissionsCtx, turn, gate, permissionsDone)
	finished := false
	var stopping chan struct{}
	defer func() {
		if stopping != nil {
			<-stopping
		}
		if !finished || err != nil {
			gate.Close()
			err = errors.Join(err, s.runtime.Stop(context.Background()))
		}
		cancelPermissions()
		<-permissionsDone
		s.toolResponses.Clear() // finalization has joined tool callbacks
		err = errors.Join(err, s.recordState())
	}()
	msg := core.UserMessage(s.reminders.PrepareInput(prompt), nil)
	if err := s.runtime.Send(msg); err != nil {
		return response, err
	}
	cancelled := ctx.Done()
	var receipt *core.TurnFinalization
	var receiptDone <-chan error
	var transportErr error
	for {
		select {
		case finalizedErr := <-receiptDone:
			// A native cancelled admission can have no terminal event. The
			// producer settles that exact receipt after unwinding; do not wait
			// forever for an event it deliberately did not emit.
			finished = true
			if finalizedErr == context.Canceled {
				finalizedErr = nil
			}
			return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, errors.Join(finalizedErr, transportErr, s.nativeSession.Session.Err())
		case <-cancelled:
			cancelled = nil
			receipt = s.runtime.Cancel()
			if receipt != nil {
				done := make(chan error, 1)
				receiptDone = done
				go func(captured *core.TurnFinalization) { done <- captured.Wait(context.Background()) }(receipt)
			} else if stopping == nil {
				stopping = make(chan struct{})
				go func(done chan struct{}) {
					_ = s.runtime.Stop(context.Background())
					close(done)
				}(stopping)
			}
		case event, ok := <-s.runtime.Outbox():
			if !ok {
				return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, errors.Join(transportErr, s.nativeSession.Session.Err())
			}
			switch e := event.(type) {
			case core.TurnEnded:
				recordErr := s.recordState()
				if e.Result.StopReason != core.StopCanceled && e.Result.StopReason != core.StopError && ctx.Err() == nil && transportErr == nil {
					recordErr = agent.EndTurn(agent.TurnCallbacks{RecordState: s.recordState,
						NextInput: func() bool { return false }, AutoPilot: func() bool { return false }, Idle: func() {}})
					decision := agent.NewStopCheck(s.hooks, e.Result.Content).Run(ctx)
					continuation, finishErr := decision.Finish(s.recordState)
					recordErr = errors.Join(recordErr, finishErr)
					e.Result.Finalization.Acknowledge(recordErr)
					if e.Result.Finalization != nil {
						recordErr = errors.Join(recordErr, e.Result.Finalization.Wait(context.Background()))
					}
					if continuation != "" && recordErr == nil && ctx.Err() == nil {
						if err := s.runtime.Send(core.UserMessage(s.reminders.PrepareInput(continuation), nil)); err != nil {
							return response, err
						}
						continue
					}
				} else {
					e.Result.Finalization.Acknowledge(recordErr)
				}
				if e.Result.Finalization != nil {
					recordErr = errors.Join(recordErr, e.Result.Finalization.Wait(context.Background()))
				}
				finished = true
				if receipt != nil {
					recordErr = errors.Join(recordErr, receipt.Wait(context.Background()))
				}
				if e.Result.StopReason == core.StopError {
					return response, errors.Join(recordErr, fmt.Errorf("agent failed: %s", e.Result.StopDetail))
				}
				stop := protocol.StopReasonEndTurn
				if ctx.Err() != nil || e.Result.StopReason == core.StopCanceled {
					stop = protocol.StopReasonCancelled
				}
				if e.Result.StopReason == core.StopMaxSteps {
					stop = protocol.StopReasonMaxTurnRequests
				}
				if e.Result.StopReason == core.StopTruncated {
					stop = protocol.StopReasonMaxTokens
				}
				if e.Result.StopReason == core.StopRefused {
					stop = protocol.StopReasonRefusal
				}
				if ctx.Err() == nil && transportErr == nil {
					transportErr = s.sendCommands(turn)
				}
				return protocol.PromptResponse{StopReason: stop}, errors.Join(recordErr, transportErr)
			case core.AgentStopped:
				if ctx.Err() != nil || transportErr != nil {
					return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, errors.Join(acpStopFailure(e.Err), transportErr, s.nativeSession.Session.Err())
				}
				if e.Err != nil && ctx.Err() == nil {
					s.hooks.ExecuteAsync(hook.StopFailure, hook.HookInput{Error: e.Err.Error()})
				}
				return response, errors.Join(e.Err, transportErr, s.nativeSession.Session.Err())
			case core.Compacted:
				agent.PostCompact(e.Trigger, s.hooks, s.reminders, s.runtimeBuildParams.FileCache, s.refreshMemory)
			case sdkagent.ToolEnd:
				s.postTool(e)
			}
			if ctx.Err() == nil && transportErr == nil {
				if update, ok := acpEvent(event); ok {
					transportErr = turn.SendUpdate(protocol.SessionNotification{Update: update})
					if transportErr != nil {
						receipt = s.runtime.Cancel()
						if receipt != nil {
							done := make(chan error, 1)
							receiptDone = done
							go func(captured *core.TurnFinalization) { done <- captured.Wait(context.Background()) }(receipt)
						} else if stopping == nil {
							stopping = make(chan struct{})
							go func(done chan struct{}) { _ = s.runtime.Stop(context.Background()); close(done) }(stopping)
						}
					}
				}
			}
		}
	}
}

// acpStopFailure suppresses only a direct, pure cancellation. Joined or
// wrapped failures retain their full evidence even when cancellation also won.
func acpStopFailure(err error) error {
	if err == context.Canceled {
		return nil
	}
	if admission, ok := err.(*sdkagent.AdmissionError); ok && admission.Boundary == "output" && admission.Err == context.Canceled {
		return nil
	}
	return err
}

func (s *acpSession) postTool(e sdkagent.ToolEnd) {
	handoff, _ := s.toolResponses.LoadAndDelete(e.ID)
	agent.PostTool(core.ToolResult{ToolCallID: e.ID, ToolName: e.Name, Content: e.Result.Content, IsError: e.Err != nil}, handoff, s.hooks, agent.WorkspaceCallbacks{TouchFile: s.runtimeBuildParams.FileCache.Touch})
}

func acpEvent(event core.Event) (protocol.SessionUpdate, bool) {
	switch e := event.(type) {
	case sdkagent.InferenceUpdate:
		if text := e.Text(); text != "" {
			return protocol.SessionUpdate{Value: protocol.SessionUpdateAgentMessageChunkVariant{SessionUpdate: "agent_message_chunk", Content: acpText(text)}}, true
		}
	case sdkagent.ToolStart:
		input := json.RawMessage(e.Args)
		if !json.Valid(input) {
			input, _ = json.Marshal(e.Args)
		}
		return protocol.SessionUpdate{Value: protocol.SessionUpdateToolCallVariant{SessionUpdate: "tool_call", ToolCallID: protocol.ToolCallID(e.ID), Title: e.Name,
			Status: protocol.Some(protocol.ToolCallStatusInProgress), RawInput: protocol.Some(input)}}, true
	case sdkagent.ToolUpdate:
		return acpToolUpdate(e.ID, e.Name, e.Partial.Content.Text(), protocol.ToolCallStatusInProgress, e.Partial.Details), true
	case sdkagent.ToolEnd:
		status := protocol.ToolCallStatusCompleted
		if e.Err != nil {
			status = protocol.ToolCallStatusFailed
		}
		return acpToolUpdate(e.ID, e.Name, e.Result.Content.Text(), status, e.Result.Details), true
	}
	return protocol.SessionUpdate{}, false
}

func acpToolUpdate(id, name, text string, status protocol.ToolCallStatus, details any) protocol.SessionUpdate {
	// ToolEnd contains the original result, before the native ResultPager.
	// Bound its display separately so a large result cannot close the transport.
	const preview = 64 << 10
	if len(text) > preview {
		end := preview
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end] + "\n[ACP display truncated; full result is in the native transcript]"
	}
	update := protocol.SessionUpdateToolCallUpdateVariant{SessionUpdate: "tool_call_update", ToolCallID: protocol.ToolCallID(id), Status: protocol.Some(status),
		Content: protocol.Some([]protocol.ToolCallContent{{Value: protocol.ToolCallContentContentVariant{Type: "content", Content: acpText(text)}}})}
	if tool.IsShellTool(name) {
		title := name
		if shell, ok := details.(toolresult.BashDetails); ok && shell.ShellLabel != "" {
			title = shell.ShellLabel
		}
		update.Title = protocol.Some(title)
	}
	return protocol.SessionUpdate{Value: update}
}
