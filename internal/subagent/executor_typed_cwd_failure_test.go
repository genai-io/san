package subagent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

func TestTypedCwdWriteFailureKeepsLiveDirectory(t *testing.T) {
	ctx := context.Background()
	initial := t.TempDir()
	next := t.TempDir()
	store, err := session.OpenStore(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	child, err := store.Create(ctx, session.CreateOptions{InitialCWD: initial})
	if err != nil {
		t.Fatal(err)
	}
	recorder := session.NewRecorder(child, "aitest")
	run := &preparedRun{cwd: initial, recording: child, recorder: recorder}
	run.forwardEvent(ctx, sdkagent.ToolStart{ID: "change"})
	putCwdHandoff(t, "change", next, run)
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	run.forwardEvent(ctx, sdkagent.ToolEnd{ID: "change"})
	if run.currentCwd() != initial {
		t.Fatal("live cwd changed without durable state")
	}
	if recorder.Err() == nil {
		t.Fatal("state persistence failure not latched")
	}
	if err := recorder.ToolAdmission(ctx, sdkagent.ToolAdmission{}); err == nil {
		t.Fatal("latched failure allowed another admission")
	}
}
