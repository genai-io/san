package subagent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestModelFallbackRejectsRecordingFailures(t *testing.T) {
	cause := errors.New("model_not_found in recording path")
	for _, failure := range []error{&ai.AdmissionError{Err: cause}, &sdkagent.AdmissionError{Err: cause}, &llm.RecordingError{Err: cause}, &hook.RecordingError{Err: cause}} {
		if shouldRetryWithParentModel(fmt.Errorf("completion: %w", failure), "missing", "parent") {
			t.Fatalf("recording failure retried: %T", failure)
		}
	}
	if !shouldRetryWithParentModel(errors.New("provider model_not_found"), "missing", "parent") {
		t.Fatal("provider fallback disabled")
	}
}
