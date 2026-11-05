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
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

func (s *acpSession) prompt(ctx context.Context, turn *acp.Turn, req protocol.PromptRequest) (response protocol.PromptResponse, err error) {
	s.input.set(turn)
	defer s.input.set(nil)
	if s.params.Plugins != nil {
		ctx = plugin.WithRegistry(ctx, s.params.Plugins)
	}
	if err := s.data.Validate(); err != nil {
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
		s.configureAgents()
		if err := s.runtime.Start(s.params, s.runtime.Messages()); err != nil {
			return response, err
		}
	}
	gate := s.runtime.PermissionGate()
	permissionsCtx, cancelPermissions := context.WithCancel(ctx)
	permissionsDone := make(chan struct{})
	go s.permissions(permissionsCtx, turn, gate, permissionsDone)
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
		s.sideEffects.Clear() // finalization has joined tool callbacks
		err = errors.Join(err, s.recordState())
	}()
	msg := core.UserMessage(s.reminders.PrepareInput(prompt), nil)
	if err := s.runtime.Send(msg); err != nil {
		return response, err
	}
	s.lastPrompt = text.String()
	cancelled := ctx.Done()
	var receipt *core.TurnFinalization
	var transportErr error
	for {
		select {
		case <-cancelled:
			cancelled = nil
			receipt = s.runtime.Cancel()
			if receipt == nil {
				// Cancellation can precede mailbox admission. Drain while shutdown joins
				// rather than returning with an accepted input still owned by the runtime.
				stopping = make(chan struct{})
				go func() {
					_ = s.runtime.Stop(context.Background())
					close(stopping)
				}()
			}
		case event, ok := <-s.runtime.Outbox():
			if !ok {
				return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, errors.Join(transportErr, s.data.Session.Err())
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
				if e.Err != nil && ctx.Err() == nil {
					s.hooks.ExecuteAsync(hook.StopFailure, hook.HookInput{Error: e.Err.Error()})
				}
				return response, errors.Join(e.Err, transportErr, s.data.Session.Err())
			case core.Compacted:
				agent.PostCompact(e.Trigger, s.hooks, s.reminders, s.params.FileCache, s.refreshMemory)
			case sdkagent.ToolEnd:
				s.postTool(e)
			}
			if ctx.Err() == nil && transportErr == nil {
				if update, ok := acpEvent(event); ok {
					transportErr = turn.SendUpdate(protocol.SessionNotification{Update: update})
					if transportErr != nil {
						receipt = s.runtime.Cancel()
					}
				}
			}
		}
	}
}

func (s *acpSession) postTool(e sdkagent.ToolEnd) {
	handoff, _ := s.sideEffects.LoadAndDelete(e.ID)
	agent.PostTool(core.ToolResult{ToolCallID: e.ID, ToolName: e.Name, Content: e.Result.Content, IsError: e.Err != nil}, handoff, s.hooks, agent.WorkspaceCallbacks{TouchFile: s.params.FileCache.Touch})
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
		return acpToolUpdate(e.ID, e.Partial.Content.Text(), protocol.ToolCallStatusInProgress), true
	case sdkagent.ToolEnd:
		status := protocol.ToolCallStatusCompleted
		if e.Err != nil {
			status = protocol.ToolCallStatusFailed
		}
		return acpToolUpdate(e.ID, e.Result.Content.Text(), status), true
	}
	return protocol.SessionUpdate{}, false
}

func acpToolUpdate(id, text string, status protocol.ToolCallStatus) protocol.SessionUpdate {
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
	return protocol.SessionUpdate{Value: protocol.SessionUpdateToolCallUpdateVariant{SessionUpdate: "tool_call_update", ToolCallID: protocol.ToolCallID(id), Status: protocol.Some(status),
		Content: protocol.Some([]protocol.ToolCallContent{{Value: protocol.ToolCallContentContentVariant{Type: "content", Content: acpText(text)}}})}}
}
