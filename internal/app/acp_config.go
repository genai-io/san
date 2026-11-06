package app

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
)

// SDK admission excludes prompts and configuration changes through this handler;
// workers cannot launch nested agents or background Bash. Wait for cleanup, not
// just terminal task status, before changing policy or hooks.
func (s *acpSession) requireConfigIdle() error {
	if !s.params.Tasks.Idle() {
		return &acp.Error{Code: -32001, Message: "Session busy: background tasks or worker cleanup are still active"}
	}
	return nil
}

func (s *acpSession) configOptions() []protocol.SessionConfigOption {
	options := protocol.SessionConfigSelectOptionsUngroupedVariant{}
	for _, model := range s.models {
		name := model.Name
		if name == "" {
			name = model.ID
		}
		options = append(options, protocol.SessionConfigSelectOption{Value: protocol.SessionConfigValueID(model.ID), Name: name})
	}
	return []protocol.SessionConfigOption{{Value: protocol.SessionConfigOptionSelectVariant{
		ID: "model", Name: "Model", Type: "select", Category: protocol.Some(protocol.SessionConfigOptionCategory("model")),
		CurrentValue: protocol.SessionConfigValueID(s.params.ModelID), Options: protocol.SessionConfigSelectOptions{Value: options},
	}}}
}

func (h *acpHost) setConfig(ctx context.Context, request protocol.SetSessionConfigOptionRequest) (protocol.SetSessionConfigOptionResponse, error) {
	req, ok := request.Value.(protocol.SetSessionConfigOptionRequestValueIDVariant)
	if !ok || req.ConfigID != "model" {
		return protocol.SetSessionConfigOptionResponse{}, &acp.Error{Code: -32602, Message: "unsupported configuration option; only model selection is available"}
	}
	h.mu.Lock()
	s := h.sessions[req.SessionID]
	h.mu.Unlock()
	if s == nil {
		return protocol.SetSessionConfigOptionResponse{}, fmt.Errorf("unknown session")
	}
	if err := s.requireConfigIdle(); err != nil {
		return protocol.SetSessionConfigOptionResponse{}, err
	}
	id := string(req.Value)
	found := false
	for _, model := range s.models {
		if model.ID == id {
			found = true
		}
	}
	if !found {
		return protocol.SetSessionConfigOptionResponse{}, &acp.Error{Code: -32602, Message: "model was not offered by this session"}
	}
	if err := ctx.Err(); err != nil {
		return protocol.SetSessionConfigOptionResponse{}, err
	}
	if id != s.params.ModelID {
		// Validate the model before stopping the idle runtime.
		if _, err := s.params.Provider.Client(id, nil); err != nil {
			return protocol.SetSessionConfigOptionResponse{}, fmt.Errorf("selected model is unavailable")
		}
		if err := s.runtime.Stop(ctx); err != nil {
			return protocol.SetSessionConfigOptionResponse{}, err
		}
		if err := s.data.Session.Append(ctx, transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StatePatch{Model: new(id)}}); err != nil {
			return protocol.SetSessionConfigOptionResponse{}, err
		}
		s.params.ModelID = id
		s.hooks.SetLLMCompleter(buildHookCompleter(s.params.Provider), id)
	}
	return protocol.SetSessionConfigOptionResponse{ConfigOptions: s.configOptions()}, nil
}
