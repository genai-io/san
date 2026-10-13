package llm

import (
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestRequestMessagesFiltersNestedImagesWithoutChangingHistory(t *testing.T) {
	image := ai.ImageBlock(ai.Image{MediaType: "image/png", Data: "aW1n"})
	messages := []core.Message{
		{ID: "user", Role: ai.RoleUser, Content: ai.Content{ai.TextBlock("before"), image, ai.TextBlock("after")}},
		{ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "c", Content: ai.Content{image, ai.TextBlock("output")}})}},
	}
	filtered := NewClient(textOnlyProvider{}, "m", 100).RequestMessages(messages)
	if len(filtered[0].Content) != 2 || filtered[0].Text() != "beforeafter" || len(filtered[1].ToolResults()[0].Content) != 1 {
		t.Fatalf("text-only request did not filter images: %+v", filtered)
	}
	if len(messages[0].Content) != 3 || len(messages[1].ToolResults()[0].Content) != 2 {
		t.Fatal("request filtering changed native history")
	}
	vision := NewClient(plainProvider{}, "m", 100).RequestMessages(messages)
	if !reflect.DeepEqual(vision, messages) {
		t.Fatal("vision request changed native blocks")
	}
}
