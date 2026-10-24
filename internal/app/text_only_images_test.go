package app

import (
	"github.com/genai-io/sdk-go/pkg/ai"

	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/todo"
)

// textOnlyModel returns a model whose session is live on a text-only provider,
// with a queue ready to accept items and a session store under a scratch HOME
// for the files a pasted image needs.
func textOnlyModel(t *testing.T) (*model, *textOnlyStubProvider) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	provider := &textOnlyStubProvider{restartStubProvider{requests: make(chan []core.Message, 2)}}
	sess := &agent.Runtime{}
	if err := sess.Start(agent.BuildParams{Provider: provider, ModelID: "deepseek-chat"}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(sess.Stop)

	m := &model{
		services: services{
			Agent:     sess,
			LLM:       &llm.Conn{},
			Tracker:   todo.NewStore(),
			Subagent:  subagent.NewRegistry(),
			Reminder:  reminder.NewService(),
			sessionID: "sess-1",
		},
		conv: conv.NewModel(80),
	}
	m.env.LLMProvider = provider
	m.env.CurrentModel = &llm.CurrentModelInfo{ModelID: "deepseek-chat"}
	m.userInput.Queue = input.NewQueue()
	return m, provider
}

func chartImage() core.Attachment {
	return core.Attachment{Image: ai.Image{MediaType: "image/png", Data: "ZmFrZQ==", FileName: "chart.png"}, Path: "/tmp/chart.png"}
}

// brokenImagePath writes a broken.png the loader rejects, points the model's
// cwd at it, and returns its absolute path.
func brokenImagePath(t *testing.T, m *model) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "broken.png")
	if err := os.WriteFile(path, []byte("not an image"), 0o644); err != nil {
		t.Fatalf("write broken.png: %v", err)
	}
	m.env.CWD = dir
	return path
}

// A text-only model gets an inspectable image path in its input text. The
// attachment stays native and is filtered only when making the request.
func TestDescribeImagesAddsPathsForTextOnlyModels(t *testing.T) {
	m, _ := textOnlyModel(t)

	content := m.describeImages("what does this show", []core.Attachment{chartImage()})

	if !strings.Contains(content, "[Image #1: /tmp/chart.png]") {
		t.Fatalf("content = %q, want the image path inlined", content)
	}
	if !strings.Contains(content, "what does this show") {
		t.Fatalf("content = %q, want the user's own text kept", content)
	}
}

func TestDescribeImagesGivesVisionModelAReadablePath(t *testing.T) {
	m, _ := textOnlyModel(t)
	m.env.LLMProvider = &restartStubProvider{} // no ImageSupportProvider — images supported

	content := m.describeImages("what does this show", []core.Attachment{chartImage()})

	if content != "what does this show\n\n[Image #1: /tmp/chart.png]" {
		t.Fatalf("content = %q, want a path that survives compaction", content)
	}
}

// Queued turns use the same per-inference filter as direct submissions.
func TestReleasedQueuedImageNeverReachesATextOnlyProvider(t *testing.T) {
	m, provider := textOnlyModel(t)
	m.userInput.Queue.Enqueue("what does this show", []core.Attachment{chartImage()})

	cmd, released := m.releaseQueuedMessage()
	if !released {
		t.Fatal("queued message was not released")
	}
	runCmd(cmd)

	select {
	case chain := <-provider.requests:
		sent := false
		for _, msg := range chain {
			if imgCount(msg) > 0 {
				t.Fatalf("provider received %d image part(s) it rejects: %+v", imgCount(msg), msg)
			}
			if strings.Contains(msg.Text(), "/tmp/chart.png") {
				sent = true
			}
		}
		if !sent {
			t.Fatalf("provider never saw the image path, so the no-images check proves nothing: %+v", chain)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued message never reached the provider")
	}

	// The conversation keeps the image: it still renders, and a later switch to
	// a vision-capable model can use it.
	last := m.conv.Messages[len(m.conv.Messages)-1]
	if len(last.Images) != 1 {
		t.Fatalf("conversation message carries %d image(s), want 1: %+v", len(last.Images), last)
	}
	if chain := m.services.Agent.Messages(); len(chain) == 0 || imageBlocks(chain[0]) != 1 {
		t.Fatal("text-only request filtering erased the native attachment")
	}
}

func TestMixedPastedAndFileImagesKeepBothOnAgentSend(t *testing.T) {
	m, _ := textOnlyModel(t)
	m.env.LLMProvider = &restartStubProvider{}
	m.env.CWD = t.TempDir()
	file, err := os.Create(filepath.Join(m.env.CWD, "file.png"))
	if err != nil {
		t.Fatal(err)
	}
	pic := image.NewRGBA(image.Rect(0, 0, 2, 2))
	pic.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(file, pic); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	m.userInput.Images.Pending = []input.PendingImage{{ID: 1, Data: chartImage()}}

	chat, ok := m.buildUserMessage("compare [Image #1] with @file.png")
	if !ok {
		t.Fatal("could not build a message containing both image sources")
	}
	chat.Content = m.describeImages(chat.Content, chat.Images)
	msg, _ := chat.ToMessage()
	if imgCount(msg) != 2 {
		t.Fatalf("agent-bound message has %d images, want paste and file", imgCount(msg))
	}
	if len(msg.Content) < 2 || msg.Content[0].Type != ai.BlockText || msg.Content[1].Type != ai.BlockImage {
		t.Fatalf("image lost its inline position: %+v", msg.Content)
	}
	if !strings.Contains(msg.Text(), filepath.Join(m.env.CWD, "file.png")) {
		t.Fatalf("source path missing from model text: %q", msg.Text())
	}
}

func TestCanceledStreamDrainPreservesQueuedAndDraftImages(t *testing.T) {
	m, provider := textOnlyModel(t)
	m.userInput = input.New("", 80, nil, input.SelectorDeps{})
	m.services.Setting = setting.New(setting.NewData())
	m.services.Hook = hook.NewEngine(nil, "", "", "")
	m.agentEvolveCaps = m.selfLearnCapabilities()
	m.agentDisabledToolsSignature = m.disabledToolsSignature()
	m.agentAutoMemory = m.autoMemory()
	queued := chartImage()
	queued.Path = "/tmp/queued.png"
	queued.FileName = "queued.png"
	m.userInput.Queue.Enqueue("look at [Image #1]", []core.Attachment{queued})
	m.userInput.Textarea.SetValue("next [Image #2]")
	m.userInput.Images.Pending = []input.PendingImage{{ID: 2, Data: chartImage()}}
	m.userInput.Images.NextID = 2

	cmd := m.drainInputQueueWhileIdle()
	if cmd == nil {
		t.Fatal("queued turn was not drained")
	}
	runCmd(cmd)
	select {
	case request := <-provider.requests:
		if !strings.Contains(request[len(request)-1].Text(), "/tmp/queued.png") {
			t.Fatal("queued image path missing from inference")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued turn did not reach inference")
	}
	if chain := m.services.Agent.Messages(); len(chain) == 0 || imgCount(chain[0]) != 1 {
		t.Fatal("queued inline image was dropped from native history")
	}
	if got := m.userInput.Textarea.Value(); got != "next [Image #2]" {
		t.Fatalf("draft = %q, want the original draft", got)
	}
	if pending := m.userInput.Images.Pending; len(pending) != 1 || pending[0].ID != 2 || pending[0].Data.FileName != "chart.png" {
		t.Fatalf("draft image = %+v, want the original pending attachment", pending)
	}
}

// The queued text keeps the [Image #N] markers the textarea used to position
// its attachments: they belong in the display, not in what the model reads —
// the same split buildUserMessage makes on the direct path. Run on a vision
// model so the only thing separating the two fields is the marker.
func TestReleasedQueuedMessageSplitsDisplayFromContent(t *testing.T) {
	m, _ := textOnlyModel(t)
	m.env.LLMProvider = &restartStubProvider{}
	m.userInput.Queue.Enqueue("look at [Image #1] please", []core.Attachment{chartImage()})

	if _, released := m.releaseQueuedMessage(); !released {
		t.Fatal("queued message was not released")
	}

	last := m.conv.Messages[len(m.conv.Messages)-1]
	if strings.Contains(last.Content, "[Image #1]") {
		t.Errorf("content = %q, want the marker stripped for the model", last.Content)
	}
	if !strings.Contains(last.DisplayContent, "[Image #1]") {
		t.Errorf("display content = %q, want the marker kept for rendering", last.DisplayContent)
	}
}

func TestQueuedInlineImageKeepsItsPathForTextOnlyInference(t *testing.T) {
	m, provider := textOnlyModel(t)
	m.userInput.Queue.Enqueue("look at [Image #1] please", []core.Attachment{chartImage()})
	cmd, released := m.releaseQueuedMessage()
	if !released {
		t.Fatal("queued inline image was not released")
	}
	runCmd(cmd)
	select {
	case request := <-provider.requests:
		last := request[len(request)-1]
		if !strings.Contains(last.Text(), "/tmp/chart.png") || strings.Contains(last.Text(), "[Image #1] please") || imageBlocks(last) != 0 {
			t.Fatalf("text-only inline image lost its path or filtering: %+v", last)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued inline image did not reach inference")
	}
	if chain := m.services.Agent.Messages(); len(chain) == 0 || imageBlocks(chain[0]) != 1 {
		t.Fatal("queued inline image was removed from native history")
	}
}

// Compaction summarizes on the active model, so it is a third way the
// conversation's images reach the provider — and the conversation now keeps
// images even when the model can't read them.
func TestCompactRequestCarriesNoImagesForTextOnlyModel(t *testing.T) {
	m, _ := textOnlyModel(t)
	m.conv.Append(core.ChatMessage{
		Role:    core.ChatUser,
		Content: "what does this show",
		Images:  []core.Attachment{chartImage()},
	})

	m.services.Agent.SetMessages([]core.Message{core.UserMessage("what does this show", []core.Attachment{chartImage()})})
	req := m.BuildCompactRequest("", "manual")

	if len(req.Messages) == 0 {
		t.Fatal("compact request has no messages, so the check below proves nothing")
	}
	for _, msg := range req.Messages {
		if imgCount(msg) > 0 {
			t.Fatalf("compact request carries %d image part(s) the provider rejects: %+v", imgCount(msg), msg)
		}
	}
	// The conversation keeps its own copy — only the request is stripped.
	if len(m.conv.Messages[0].Images) != 1 {
		t.Error("building the request mutated the conversation's images")
	}
}

// Releasing a queued message runs mid-stream, when the textarea holds the next
// message being typed. A queued image that fails to load costs the user that
// image — not both messages.
func TestQueuedImageErrorLeavesTheNextMessageAlone(t *testing.T) {
	m, _ := textOnlyModel(t)
	brokenImagePath(t, m)
	m.userInput.Queue.Enqueue("look at @broken.png", nil)
	// What the user is typing right now, while the turn streams.
	m.userInput.Images.Pending = []input.PendingImage{{ID: 1, Data: core.Attachment{Image: ai.Image{FileName: "next.png"}}}}

	if _, released := m.releaseQueuedMessage(); !released {
		t.Fatal("queued message was not released")
	}

	if len(m.userInput.Images.Pending) != 1 {
		t.Fatalf("pending images = %+v, want the one being typed to survive", m.userInput.Images.Pending)
	}
	last := m.conv.Messages[len(m.conv.Messages)-1]
	if !strings.Contains(last.Content, "look at") {
		t.Fatalf("last message = %q, want the queued message sent anyway", last.Content)
	}
}

// A queued message that leads with an image path the loader rejects still has
// to lose the path: the release path can't hand the turn back to the textarea,
// so whatever it sends is final, and a bare path left in the text is the
// unknown-command-looking string this whole change exists to remove.
func TestQueuedLeadingImageFailureStillConsumesThePath(t *testing.T) {
	m, _ := textOnlyModel(t)
	broken := brokenImagePath(t, m)
	m.userInput.Queue.Enqueue(broken+" what is this", nil)

	if _, released := m.releaseQueuedMessage(); !released {
		t.Fatal("queued message was not released")
	}

	last := m.conv.Messages[len(m.conv.Messages)-1]
	if strings.Contains(last.Content, broken) {
		t.Fatalf("last message = %q, want the failed leading path consumed, not sent as text", last.Content)
	}
	if !strings.Contains(last.Content, "what is this") {
		t.Fatalf("last message = %q, want the rest of the prompt to survive", last.Content)
	}
}

// The inlined path is written into content that conv persists and replays, so
// the file behind it has to outlive not just the turn but the process: a
// --continue replays the same path from a fresh one. It lives with the
// session's other blobs and goes when the session does, for both vision and
// text-only models.
func TestPastedImageFileLivesWithTheSession(t *testing.T) {
	for _, tc := range []struct {
		name   string
		vision bool
	}{
		{name: "text-only"},
		{name: "vision", vision: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := textOnlyModel(t)
			if tc.vision {
				m.env.LLMProvider = &restartStubProvider{}
			}
			pasted := core.Attachment{Image: ai.Image{MediaType: "image/png", Data: "ZmFrZQ==", FileName: "clipboard_120000.png"}}

			content := m.describeImages("what does this show", []core.Attachment{pasted})
			store := m.services.SessionStore
			entries, err := os.ReadDir(store.ImagesDir("sess-1"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("session image dir holds %v, %v; want the one pasted file", entries, err)
			}
			path := filepath.Join(store.ImagesDir("sess-1"), entries[0].Name())
			if !strings.Contains(content, "[Image #1: "+path+"]") {
				t.Fatalf("content = %q, want the session-blob path %q inlined", content, path)
			}
			if got, _ := os.ReadFile(path); string(got) != "fake" {
				t.Fatalf("read %s = %q; want the pasted bytes", path, got)
			}

			if err := store.Delete("sess-1"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("stat %s after deleting the session = %v, want it gone with the transcript", path, err)
			}
		})
	}
}

func imgCount(m core.Message) int {
	n := 0
	for _, b := range m.Content {
		if b.Type == ai.BlockImage {
			n++
		}
	}
	return n
}
