---
package: github.com/genai-io/gen-code/internal/instruction
layer: feature
---

# instruction

Discovers and loads runtime instruction documents from Gen Code and compatible
CLI file formats.

## Purpose

The app uses this package to populate memory reminders and to resolve which
document `/memory edit` opens. It owns filename compatibility, precedence,
templates, `@import` expansion, and `.gen/rules` / local instruction loading
so those decisions do not leak into `internal/core` or UI code.

## Contract

```go
package instruction

type Format struct {
	ID              string
	GlobalPaths     func(home string) []string
	ProjectPaths    func(cwd string) []string
	NewProjectPath  func(cwd string) string
	ProjectTemplate func(projectName string) string
}

type File struct {
	Path    string
	Size    int64
	Content string
	Level   string
}

type Paths struct {
	Global       []string
	GlobalRules  string
	Project      []string
	ProjectRules string
	Local        []string
}

func Formats() []Format
func Load(cwd string) (user, project string)
func LoadFiles(cwd string) []File
func AllPaths(cwd string) Paths
func ProjectFile(cwd, formatID string) (string, bool)
func ProjectTemplate(cwd, formatID string) (string, bool)
func GlobalTemplate() string
func LocalTemplate() string
func RulesTemplate() string
func FindActiveFile(paths []string) string
func FindExisting(paths []string) string
func ListRulesFiles(rulesDir string) []string
func FileSize(path string) int64
func FormatFileSize(size int64) string
```

## Internals

- Registered `Format` values are ordered `gen`, `claude`, `codex`; the first
  non-empty primary document found in that order wins per scope.
- `/init --<format-id>` resolves against the same registry; adding a future
  compatible filename does not require a separate UI dispatch branch.
- Project candidates are `.gen/GEN.md`, `GEN.md`, `.claude/CLAUDE.md`,
  `CLAUDE.md`, then `AGENTS.md`; global candidates use the matching home dirs.
- Runtime loading and memory UI selection skip empty higher-precedence
  candidates; an empty draft remains editable only when no active candidate
  exists in its scope.
- `.gen/rules/*.md` and `.gen/GEN.local.md` remain Gen Code extensions and are
  appended after the selected main documents.
- `@file.md` imports are bounded, cycle-protected, and restricted to the
  containing directory tree.

## Lifecycle

The package has no mutable registry or goroutines. App handlers load directly
from disk at session start, context refresh, and command execution; reminder
providers retain only the rendered instruction content.

## Tests

```
internal/instruction/instruction_test.go — precedence, Codex fallback, templates, imports, rules and local loading.
internal/app/input/on_memory_test.go      — /init and /memory edit behavior using discovered paths.
```

## See Also

- Runtime delivery: [`reminder.md`](reminder.md)
- Channel model: [`../concepts/harness-channels.md`](../concepts/harness-channels.md)
- Layer rules: [`../reference/dependency-rules.md`](../reference/dependency-rules.md)
