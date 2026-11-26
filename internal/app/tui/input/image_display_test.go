package input

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestImageDisplayKeepsFileReferencesInSentence(t *testing.T) {
	first := core.Attachment{Image: ai.Image{Data: "first"}, Path: "/work/first.png"}
	second := core.Attachment{Image: ai.Image{Data: "second"}, Path: "/work/second.png"}
	display, images := ImageDisplay("/work", "Compare @first.png with @second.png briefly.", nil, []core.Attachment{first, second})
	if display != "Compare [Image #1] with [Image #2] briefly." {
		t.Fatalf("display = %q", display)
	}
	if len(images) != 2 || images[0].Data != "first" || images[1].Data != "second" {
		t.Fatalf("images = %+v, want first then second", images)
	}
}

func TestImageDisplayOrdersFilesWithPastedImages(t *testing.T) {
	pasted := core.Attachment{Image: ai.Image{Data: "pasted"}}
	file := core.Attachment{Image: ai.Image{Data: "file"}, Path: "/work/file.png"}
	display, images := ImageDisplay("/work", "Compare @file.png with [Image #7]", []core.Attachment{pasted}, []core.Attachment{file})
	if display != "Compare [Image #1] with [Image #2]" {
		t.Fatalf("display = %q", display)
	}
	if len(images) != 2 || images[0].Data != "file" || images[1].Data != "pasted" {
		t.Fatalf("images = %+v, want file then pasted", images)
	}
	chat := core.ChatMessage{Role: core.ChatUser, Content: "Compare  with", DisplayContent: display, Images: images}
	msg, _ := chat.ToMessage()
	if len(msg.Content) != 4 || msg.Content[1].Image.Data != "file" || msg.Content[3].Image.Data != "pasted" {
		t.Fatalf("native transcript content lost original order: %+v", msg.Content)
	}
}

func TestImageDisplayPreservesUnreferencedAndBareImages(t *testing.T) {
	bare := core.Attachment{Image: ai.Image{Data: "bare"}, Path: "/work/file.png"}
	unreferenced := core.Attachment{Image: ai.Image{Data: "old-queued"}}
	display, images := ImageDisplay("/work", "look at /work/file.png please", []core.Attachment{unreferenced}, []core.Attachment{bare})
	if !strings.Contains(display, "/work/file.png[Image #1]") {
		t.Fatalf("bare file path disappeared: %q", display)
	}
	if len(images) != 2 || images[0].Data != "bare" || images[1].Data != "old-queued" {
		t.Fatalf("images = %+v, want bare and unreferenced queued", images)
	}
	msg, _ := (core.ChatMessage{Role: core.ChatUser, DisplayContent: display, Content: "look at /work/file.png please", Images: images}).ToMessage()
	if len(msg.Content) != 4 || msg.Content[1].Image.Data != "bare" || msg.Content[3].Image.Data != "old-queued" {
		t.Fatalf("native transcript dropped an attachment: %+v", msg.Content)
	}
}
