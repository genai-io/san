package cli

import (
	"context"
	"errors"

	apphook "github.com/genai-io/san/internal/app/hook"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

// runRecordedPrint owns persistence until the one-shot runtime, tasks and hooks
// have drained. No model call or tool admission precedes the durable identity.
func runRecordedPrint(ctx context.Context, params runtime.BuildParams, message core.Message, onText func(string)) (result *core.Result, err error) {
	store, err := session.OpenStore(ctx, params.CWD)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	owner, err := store.Create(ctx, session.CreateOptions{InitialCWD: params.CWD, Config: &session.EventConfig{Provider: new(params.Provider.Name()), Model: new(params.ModelID), Mode: new("normal"), GenerationOptions: &session.GenerationOptions{Version: 1, MaxTokens: new(params.MaxTokens)}}})
	if err != nil {
		return nil, err
	}
	params.SessionID, params.Recording = owner.ID(), owner
	if params.Skills == nil {
		params.Skills = skill.DefaultIfInit()
	}
	if params.Skills != nil {
		params.Skills.SetStateChangeObserver(session.NewRecorder(owner, params.Provider.Name()).RecordSkillStateChange)
		defer params.Skills.SetStateChangeObserver(nil)
	}
	params.ResultFilter = runtime.ResultLimiter{Store: store, SessionID: owner.ID()}.Filter
	if params.Tasks == nil {
		params.Tasks = task.Default()
	}
	if params.Tracker == nil {
		params.Tracker = todo.NewStoreWithTasks(params.Tasks)
	}
	engine, _ := params.HookEngine.(*hook.Engine)
	if engine != nil {
		engine.SetSession(owner.ID(), owner.Path())
		apphook.Bind(engine, params.Provider, owner, params.ModelID)
	}
	defer func() {
		if params.Tasks != nil {
			err = errors.Join(err, params.Tasks.StopServices(""))
			params.Tasks.Cancel()
			params.Tasks.Join()
		}
		if engine != nil {
			engine.Shutdown()
		}
		projection := owner.Snapshot()
		if saved := projection.State().ExecutionCWD; saved == nil || *saved != params.CWD {
			err = errors.Join(err, owner.Append(context.Background(), session.Event{Payload: session.SessionUpdatePayload{State: &session.StatePatch{ExecutionCWD: session.SetField(params.CWD)}}}))
		}
		err = errors.Join(err, owner.Err(), owner.Close())
	}()
	return runtime.RunHeadless(ctx, params, message, onText)
}
