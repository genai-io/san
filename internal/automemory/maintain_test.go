package automemory

import (
	"context"
	"errors"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type fakeProvider struct{ driver *aitest.Driver }

func (p fakeProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.driver.Client(), nil
}
func (fakeProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (fakeProvider) Name() string                                        { return "fake" }

func request(t *testing.T, text string) Request {
	t.Helper()
	req, ok := ParseRequest(text)
	if !ok {
		t.Fatalf("did not parse %q", text)
	}
	return req
}

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseRequest(t *testing.T) {
	for _, text := range []string{
		"Remember to run tests before pushing", "Do you remember this?", "Forget everything",
		"forget memory topic.md", "forget project memory ../topic.md", "forget user memory MEMORY.md",
		"forget project memory one.md and two.md", "update user memory one.md:", "remember:",
		"> forget user memory topic.md", "forget project memory nul.md",
	} {
		if _, ok := ParseRequest(text); ok {
			t.Errorf("accepted %q", text)
		}
	}
	if req := request(t, "Remember: build with Go"); req.scope != "project" {
		t.Fatalf("default scope = %q", req.scope)
	}
}

func TestMaintainLifecycleAndScope(t *testing.T) {
	user := t.TempDir()
	project := filepath.Join(t.TempDir(), "missing", "memory")
	put(t, filepath.Join(user, "build.md"), "private unrelated scope")
	stores := system.MemoryStores{User: user, Project: project}
	driver := aitest.New(
		aitest.Says(`{"action":"remember","file":"build.md","name":"Build","description":"Build preference","type":"project","body":"Use make.\nKeep tests fast."}`),
		aitest.Says(`{"action":"update","file":"build.md","old":"Use make.","replacement":"Use go build."}`),
		aitest.Says(`{"action":"forget","file":"build.md"}`),
	)
	m := Maintainer{Provider: fakeProvider{driver}}
	for i, text := range []string{"remember: Use make; keep tests fast.", "update project memory build.md: Use go build instead", "forget project memory build.md"} {
		notice, err := m.Run(t.Context(), stores, request(t, text))
		if err != nil || notice == "" {
			t.Fatalf("%s: notice=%q, err=%v", text, notice, err)
		}
		call := driver.Last()
		if len(call.Tools) > 0 || len(call.Messages) != 1 || call.Messages[0].Role != ai.RoleUser || strings.Contains(call.Messages[0].Content.Text(), "private unrelated scope") {
			t.Fatalf("unexpected side call: %+v", call)
		}
		index := filepath.Join(project, system.AutoMemoryIndexName)
		if i == 0 {
			if !strings.Contains(get(t, index), "[Build](build.md)") {
				t.Fatal("missing index link")
			}
			put(t, index, "# Topics\n\n- [Other](other.md) — retain\n"+get(t, index))
		}
		if i == 1 && !strings.Contains(get(t, filepath.Join(project, "build.md")), "Use go build.\nKeep tests fast.") {
			t.Fatal("update did not preserve the rest of the note")
		}
		if i == 2 && get(t, index) != "# Topics\n\n- [Other](other.md) — retain\n" {
			t.Fatal("forget did not preserve unrelated index content")
		}
	}
	if _, err := os.Stat(filepath.Join(project, "build.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("forgotten file: %v", err)
	}
	if got := get(t, filepath.Join(user, "build.md")); got != "private unrelated scope" {
		t.Fatalf("other scope changed: %q", got)
	}
}

func TestRejectStaleWrites(t *testing.T) {
	for _, file := range []string{"topic.md", system.AutoMemoryIndexName} {
		t.Run(file, func(t *testing.T) {
			dir := t.TempDir()
			note := filepath.Join(dir, "topic.md")
			put(t, note, "old note")
			changed := filepath.Join(dir, file)
			driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				put(t, changed, "external edit")
				return aitest.Says(`{"action":"update","file":"topic.md","old":"old note","replacement":"model edit"}`)(ctx, req)
			})
			m := Maintainer{Provider: fakeProvider{driver}}
			notice, err := m.Run(t.Context(), system.MemoryStores{User: dir}, request(t, "update user memory topic.md: change the note"))
			if notice != "" || !errors.Is(err, errStale) || get(t, changed) != "external edit" {
				t.Fatalf("notice=%q err=%v; external edit must be preserved", notice, err)
			}
		})
	}
}

func TestRejectUnauthorizedForgetAndTools(t *testing.T) {
	for _, turn := range []aitest.Turn{
		aitest.Says(`{"action":"forget","file":"other.md"}`),
		aitest.Says(`{"action":"forget","file":"topic.md","scope":"user"}`),
		aitest.Asks(ai.ToolCall{ID: "1", Name: "Remove", Input: `{}`}),
	} {
		dir := t.TempDir()
		put(t, filepath.Join(dir, "topic.md"), "safe")
		put(t, filepath.Join(dir, "other.md"), "unrelated")
		m := Maintainer{Provider: fakeProvider{aitest.New(turn)}}
		notice, err := m.Run(t.Context(), system.MemoryStores{Project: dir}, request(t, "forget project memory topic.md"))
		if notice != "" || err == nil || get(t, filepath.Join(dir, "topic.md")) != "safe" || get(t, filepath.Join(dir, "other.md")) != "unrelated" {
			t.Fatalf("notice=%q err=%v", notice, err)
		}
	}
}

func TestRememberEvidenceAndNoOp(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Formatting](fmt.md) — formatter preference\n- [Remote](remote.md) — passwords\n")
	put(t, filepath.Join(dir, "fmt.md"), "Use gofmt for Go code.")
	put(t, filepath.Join(dir, "remote.md"), "unrelated sensitive text")
	driver := aitest.New(aitest.Says(`{"action":"none"}`))
	m := Maintainer{Provider: fakeProvider{driver}}
	notice, err := m.Run(t.Context(), system.MemoryStores{Project: dir}, request(t, "remember: Our formatter is gofmt"))
	if notice != "" || err != nil {
		t.Fatalf("notice=%q err=%v", notice, err)
	}
	body := driver.Last().Messages[0].Content.Text()
	if !strings.Contains(body, "Use gofmt for Go code.") || strings.Contains(body, "unrelated sensitive text") {
		t.Fatalf("unexpected evidence: %s", body)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("no-op created files: %v, %v", entries, err)
	}
}

func TestRejectSymlinkNote(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	put(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(dir, "topic.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	driver := aitest.New(aitest.Says(`{"action":"forget","file":"topic.md"}`))
	m := Maintainer{Provider: fakeProvider{driver}}
	notice, err := m.Run(t.Context(), system.MemoryStores{Project: dir}, request(t, "forget project memory topic.md"))
	if notice != "" || err == nil || driver.Calls() != 0 || get(t, outside) != "outside" {
		t.Fatalf("notice=%q err=%v calls=%d", notice, err, driver.Calls())
	}
}
