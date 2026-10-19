package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/fs"
)

// autoMemory is the auto-memory stores the main agent maintains, zero when
// auto memory is off (setting, or SAN_DISABLE_AUTO_MEMORY).
func (m *model) autoMemory() system.MemoryStores {
	if !m.services.Setting.AutoMemory() {
		return system.MemoryStores{}
	}
	return setting.MemoryStoresFor(m.env.CWD)
}

// autoMemoryStore returns the store a Write/Edit call targets with a markdown
// file, or "": the agent maintains its stores without prompting. Symlinks are
// resolved so a link cannot carry the write outside the store.
func autoMemoryStore(s system.MemoryStores, toolName string, args map[string]any) string {
	if toolName != "Write" && toolName != "Edit" {
		return ""
	}
	path, _ := args["file_path"].(string)
	if !strings.HasSuffix(path, ".md") || !filepath.IsAbs(path) {
		return ""
	}
	for _, dir := range []string{s.User, s.Project} {
		if dir != "" && setting.WithinDirectory(path, dir) {
			return dir
		}
	}
	return ""
}

// permitAutoMemoryWrite reports whether a call is the agent keeping its own
// memory, which needs no approval in any mode — headless and don't-ask
// included, where a prompt would become a refusal. Only a deny rule stops it.
func permitAutoMemoryWrite(s system.MemoryStores, toolName string, args map[string]any, denyRule func(string, map[string]any) string) bool {
	return autoMemoryStore(s, toolName, args) != "" && denyRule(toolName, args) == ""
}

// afterMemoryWrite runs after a successful Write/Edit into the auto-memory
// store: it stamps the file's `modified` time and, when the index changed,
// tells the model right away what is wrong with it instead of next session.
func afterMemoryWrite(s system.MemoryStores, c sdkagent.PostToolContext) *sdkagent.Result {
	if c.Err != nil {
		return nil
	}
	var args map[string]any
	if json.Unmarshal([]byte(c.Call.Input), &args) != nil {
		return nil
	}
	dir := autoMemoryStore(s, c.Call.Name, args)
	if dir == "" {
		return nil
	}
	path, _ := args["file_path"].(string)
	var notes []string
	if stamped, err := system.StampModified(path, time.Now()); err == nil && stamped {
		// The stamp is ours, not an outside change the next Edit should balk at.
		fs.RecordFileWritten(path)
		notes = append(notes, "Recorded the write time as `modified` in its frontmatter and tidied its keys; the file needs no follow-up edit.")
	}
	if dup := s.Duplicates(); len(dup) > 0 {
		notes = append(notes, "Your memory has duplicates:\n- "+strings.Join(dup, "\n- "))
	}
	if filepath.Base(path) == system.AutoMemoryIndexName {
		if _, problems := system.LoadAutoMemoryAt(dir); len(problems) > 0 {
			notes = append(notes, "Your memory index needs fixing:\n- "+strings.Join(problems, "\n- "))
		}
	}
	if len(notes) == 0 {
		return nil
	}
	out := c.Result
	out.Content = ai.TextContent(strings.TrimSpace(c.Result.Content.Text() + "\n\n" + strings.Join(notes, "\n\n")))
	return &out
}

// memoryRecap is the turn-end card naming the memories the turn saved or
// updated, drawn like the self-learning recap; "" when it wrote none.
func memoryRecap(s system.MemoryStores, changes []conv.MemoryChange) string {
	var actions []ReviewAction
	for _, c := range changes {
		store := "project"
		if filepath.Dir(c.Path) == s.User {
			store = "user"
		}
		target := c.Description
		if target == "" {
			target = strings.TrimSuffix(filepath.Base(c.Path), ".md")
		}
		actions = append(actions, ReviewAction{Kind: "memory", Target: target, Note: c.Verb + " · " + store + " store"})
	}
	return formatRecapBlock(actions, "")
}
