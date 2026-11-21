package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/genai-io/san/internal/automemory"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
)

const preTurnRecallBudget = 1500 * time.Millisecond

// PreparedMemory holds recall open until the host accepts the enriched input.
// Maintenance is handled entirely on the side and never submitted to main.
type PreparedMemory struct {
	Message    core.Message
	Completion *session.Completion
	Handled    bool
	Notice     string
	Change     *automemory.Change // Explicit maintenance, not yet admitted.
}

// NeedsMemoryPreparation lets hosts bypass side work when there is nothing to prepare.
func NeedsMemoryPreparation(stores system.MemoryStores, message core.Message) bool {
	_, explicit := maintenanceRequest(message)
	return explicit || stores.HasNotes()
}

func maintenanceRequest(message core.Message) (automemory.Request, bool) {
	if message.Content.HasImages() {
		return automemory.Request{}, false
	}
	return automemory.ParseRequest(message.Text())
}

// IsMemoryMaintenance reports whether the input requests an explicit store change.
func IsMemoryMaintenance(message core.Message) bool {
	_, ok := maintenanceRequest(message)
	return ok
}

// Prepare admits explicit maintenance immediately for synchronous hosts.
func (r Recaller) Prepare(ctx context.Context, stores system.MemoryStores, message core.Message, history []core.Message) (PreparedMemory, error) {
	if IsMemoryMaintenance(message) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	prepared, err := r.PrepareForAdmission(ctx, stores, message, history)
	if err == nil && prepared.Change != nil {
		prepared.Notice, err = prepared.Change.Apply(ctx)
		prepared.Change = nil
	}
	return prepared, err
}

// PrepareForAdmission does not write to memory. The host must apply or discard
// any returned change when it resolves admission of the original input.
func (r Recaller) PrepareForAdmission(ctx context.Context, stores system.MemoryStores, message core.Message, history []core.Message) (prepared PreparedMemory, err error) {
	prepared.Message = message
	if req, ok := maintenanceRequest(message); ok {
		prepared.Handled = true
		if stores.Off() {
			prepared.Notice = "Auto Memory is off."
			return prepared, nil
		}
		if r.Provider == nil {
			prepared.Notice = "Connect a model to update Auto Memory."
			return prepared, nil
		}
		maintainer := automemory.Maintainer{Provider: r.Provider, Model: r.Model, Effort: r.Effort, Recording: r.Recording}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		prepared.Change, err = maintainer.Propose(ctx, stores, req)
		return prepared, err
	}
	if stores.Off() || r.Provider == nil || strings.TrimSpace(message.Text()) == "" {
		return prepared, ctx.Err()
	}
	lookup, cancel := context.WithTimeout(ctx, preTurnRecallBudget)
	defer cancel()
	scope := r.Recording.Completion(session.PurposeRecall)
	defer func() {
		if prepared.Completion == nil {
			if closeErr := scope.Close(); closeErr != nil {
				err = errors.Join(err, &llm.RecordingError{Err: closeErr})
			}
		}
	}()
	notes, err := r.selectNotes(lookup, stores, message.Text(), scope)
	if err != nil {
		return prepared, err
	}
	if ctx.Err() != nil {
		return prepared, ctx.Err()
	}
	if lookup.Err() != nil || len(notes) == 0 {
		return prepared, nil
	}
	existing := make(map[string]bool)
	for _, msg := range history {
		for _, block := range reminder.Blocks(msg.Text()) {
			if block.Source == reminder.ProviderMemoryRecall {
				existing[block.Text] = true
			}
		}
	}
	var blocks []string
	for _, note := range notes {
		if block := recalledNote(stores, note); block != "" && !existing[block] {
			blocks = append(blocks, block)
		}
	}
	if ctx.Err() != nil {
		return prepared, ctx.Err()
	}
	if lookup.Err() == nil && len(blocks) > 0 {
		prepared.Message.Content = reminder.AppendBlocks(message.Content, blocks...)
		prepared.Completion = scope
	}
	return prepared, nil
}

func recalledNote(stores system.MemoryStores, note system.Memory) string {
	if !stores.Contains(note.Path) {
		return ""
	}
	root, err := os.OpenRoot(filepath.Dir(note.Path))
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(note.Path)
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	f, err := root.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()
	const maxBytes = 2048
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(data) == 0 {
		return ""
	}
	body := strings.TrimSpace(strings.ToValidUTF8(string(data[:min(len(data), maxBytes)]), "�"))
	if len(data) > maxBytes {
		body += "\n[Note preview truncated.]"
	}
	return reminder.WrapWithSource(fmt.Sprintf("Relevant memory %s (saved %s):\n%s", memoryID(note), info.ModTime().Format(time.DateOnly), body), reminder.ProviderMemoryRecall)
}
