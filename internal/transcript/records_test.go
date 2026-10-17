package transcript

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// A Record is written whole as an application record's data; its ID is
// assigned on read and its messages are the log's own, so neither is in it.
func TestRecordJSONLeavesIDAndMessagesToTheLog(t *testing.T) {
	rec := Record{
		ID:        "rec-1",
		SessionID: "tx-1",
		Type:      InferenceRequested,
		GitBranch: "main",
		Message:   &ai.Message{ID: "msg-1"},
		Messages:  []ai.Message{{ID: "msg-2"}},
		Inference: &InferenceRecord{Sequence: 1},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var got Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "" || got.Message != nil || got.Messages != nil {
		t.Fatalf("serialized what the log owns: %s", data)
	}
	if got.SessionID != "tx-1" || got.GitBranch != "main" || got.Inference == nil || got.Inference.Sequence != 1 {
		t.Fatalf("round-trip mismatch: %s", data)
	}
}

func TestToolSchemaUsesSDKShape(t *testing.T) {
	rec := Record{
		ID:        "rec-1",
		SessionID: "tx-1",
		Type:      ToolAdded,
		Tool: &ToolRecord{Schema: &ai.Schema{
			Name:        "Read",
			Description: "",
			Definition:  json.RawMessage(`{"type":"object"}`),
		}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("invalid json: %s", data)
	}
	if got := string(data); !containsAll(got, `"definition":{"type":"object"}`) {
		t.Fatalf("tool schema JSON did not preserve model shape: %s", got)
	}

}

func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(s, n) {
			return false
		}
	}
	return true
}
