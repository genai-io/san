---
package: github.com/genai-io/san/internal/setting
layer: feature
---

# setting

Data loader, merger, and the central permission decision gate.
Reads `~/.san/settings.json` and `<project>/.san/settings.json`, merges
project-over-user with documented precedence, and decides allow / deny /
ask for every tool call.

## Purpose

Two concerns live here:

1. **Configuration**: load and merge two-tier settings (user + project),
   plus hooks, disabled tools, search provider, permission rules, env
   vars, work directory, and Claude Code-compatible `.claude/` shims.
2. **Permission decisions**: `HasPermissionToUseTool` is the
   authoritative gate every tool call passes through. Decision sources
   include explicit rules, suggestions from hooks, session-scoped
   permissions, and bypass-mode policy.

## Contract

Data loader + central permission decision gate. *Settings wraps *Data under a mutex; methods are mutex-protected views. The package exposes `*Settings` directly — no Service interface.

```go
package setting

// Settings is the opaque handle. Type exported; fields unexported.
type Settings struct { /* internal fields */ }

func (s *Settings) Snapshot() *Data
func (s *Settings) AllowBypass() bool
func (s *Settings) IsGitRepo(cwd string) bool
func (s *Settings) Reload(cwd string) error
func (s *Settings) DisabledTools() map[string]bool
func (s *Settings) SearchProvider() string
func (s *Settings) SetSearchProvider(provider string)
func (s *Settings) Hooks() map[string][]Hook
func (s *Settings) CheckPermission(toolName string, args map[string]any, session *SessionPermissions) PermissionBehavior
func (s *Settings) HasPermissionToUseTool(toolName string, args map[string]any, session *SessionPermissions) PermissionDecision
func (s *Settings) ResolveHookAllow(toolName string, args map[string]any, session *SessionPermissions) bool
func (s *Settings) GetDisabledToolsAt(userLevel bool) map[string]bool
func (s *Settings) UpdateDisabledToolsAt(disabledTools map[string]bool, userLevel bool) error

// Package-level access
func Initialize(opts Options)
func Default() *Settings
func DefaultIfInit() *Settings           // nil pre-Initialize
func SetDefaultSettings(s *Settings)      // test-only
func ResetDefaultSettings()              // test-only
```


## Internals

- `Data` (`settings.go`) — value type holding all merged config.
- `loader.go` + `merger.go` — read the two tiers and combine them with
  documented precedence (project overrides user, except in a few flagged
  fields).
- `permission.go` — the rule engine. Big file (19k); deserves to move out
  to a `service/permission/` package per the split above.
- `bash_ast.go` — bash command parsing for the granular Bash permission
  rules (read-only matchers like `git status` allowed but `git push`
  asked).
- `workdir.go` — cwd resolution and git-root detection.
- `security.go` — env var sanitization for hook/MCP subprocess execution.

## Lifecycle

- Construction: `Initialize(Options{CWD})` runs once at startup and after
  cwd changes.
- Reload: `Reload(cwd)` rebuilds settings under lock; the singleton swaps
  atomically.
- Per-call: permission checks are mutex-protected reads against the
  current snapshot.

## Tests

```
internal/setting/permission_test.go      — large table of permission
                                            scenarios.
internal/setting/config_extra_test.go    — config merge semantics.
internal/setting/bash_ast_test.go        — bash command parsing for
                                            permission patterns.
internal/setting/workdir_test.go         — cwd resolution.
```

## See Also

- Code: `internal/setting/`
- Reference: [`reference/configuration.md`](../../reference/configuration.md)
- Concepts: [`concepts/permission-model.md`](../../concepts/permission-model.md)
- Permission consumers: [`packages/tool.md`](tool.md), [`packages/hook.md`](hook.md)
- Layer: `feature`
