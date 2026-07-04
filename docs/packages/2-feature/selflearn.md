---
package: github.com/genai-io/san/internal/selflearn
layer: feature
---

# selflearn

Runs San's background self-learning pass: on cadence, a restricted fork reviews
recent conversation state and writes durable memory or agent-created skills.

## Purpose

`selflearn` owns the trigger logic, fork prompt, writable memory store, and
restricted skill-management surface for the L1 background review loop. The app
wires it per session after resolving settings; the package itself stays outside
the Bubble Tea model and exposes concrete types for the pieces the app needs.

This package deliberately writes only to San-managed durable state: project
memory under the encoded project configuration directory and skills marked with
agent-created provenance. User-created skills remain read-only unless the
setting explicitly opts into updates.

## Contract

The package exposes concrete structs plus one callback type. There is no
producer-side service interface; callers construct the exact components they
need.

```go
package selflearn

type Config struct {
    Memory         Arm
    Skills         Arm
    Perms          ActionPermissions
    MemoryMaxChars int
}

func (c Config) Enabled() bool
func ResolveSettings(s setting.SelfLearnSettings) (Config, error)

type Arm struct {
    Enabled  bool
    Interval int
}

type ReviewKind uint8

const (
    KindMemory ReviewKind = 1 << iota
    KindSkills
)

func (k ReviewKind) Has(x ReviewKind) bool
func (k ReviewKind) String() string

type ReviewFunc func(kinds ReviewKind, snapshot []core.Message)

type Reviewer struct { /* unexported */ }

func New(cfg Config, review ReviewFunc) *Reviewer
func (r *Reviewer) SeedTurns(priorUserTurns int)
func (r *Reviewer) Observe(result core.Result)

type ForkConfig struct {
    LLM     core.LLM
    System  *system.System
    CWD     string
    Memory  *MemoryStore
    Skills  *SkillManager
    OnEvent func(core.Event)
}

func RunReview(ctx context.Context, fc ForkConfig, kinds ReviewKind, snapshot []core.Message) (string, error)

type MemoryStore struct { /* unexported */ }

func NewMemoryStore(cwd string, maxFile int) *MemoryStore
func (s *MemoryStore) MaxKB() int
func (s *MemoryStore) SetWriteObserver(fn MemoryWriteObserver)
func (s *MemoryStore) Dir() string
func (s *MemoryStore) Add(file, content, note string) (string, error)
func (s *MemoryStore) Replace(file, oldText, newContent, note string) (string, error)
func (s *MemoryStore) Remove(file, oldText, note string) (string, error)

type ActionPermissions struct {
    AllowCreate            bool
    AllowUpdate            bool
    AllowDelete            bool
    AllowUpdateUserCreated bool
}

func DefaultActionPermissions() ActionPermissions

type SkillManager struct { /* unexported */ }

func NewSkillManager(cwd string, perms ActionPermissions) *SkillManager
func (m *SkillManager) Perms() ActionPermissions
func (m *SkillManager) SetWriteObserver(fn SkillWriteObserver)
func (m *SkillManager) Inventory() []SkillInfo
func (m *SkillManager) Create(name, description, body, level, note string) (string, error)
func (m *SkillManager) Edit(name, body, note string) (string, error)
func (m *SkillManager) Patch(name, oldText, newText string, replaceAll bool, note string) (string, error)
func (m *SkillManager) WriteFile(name, file, content, note string) (string, error)
func (m *SkillManager) RemoveFile(name, file, note string) (string, error)
func (m *SkillManager) Delete(name, note string) (string, error)
```

## Internals

- `Reviewer` owns cadence counters and the at-most-one-in-flight gate. It
  observes completed `core.Result` values and launches the injected
  `ReviewFunc` on a background goroutine.
- `RunReview` builds a restricted review agent with only `memory_write` and
  `skill_manage`, trims trailing pending messages, and runs with a fixed step
  and wall-clock budget.
- `MemoryStore` writes delimited markdown entries under
  `system.AutoMemoryDir(cwd)`, with traversal checks, prompt-injection scans,
  and per-file size caps.
- `SkillManager` reads and writes San skill directories directly so the review
  sees its own mid-session writes without relying on the startup skill
  registry cache.
- Write observers are used by `internal/app` to update the live self-learning
  indicator and final recap.

## Lifecycle

- Construction: `internal/app` resolves `setting.SelfLearnSettings`, creates a
  session-scoped `Reviewer`, `MemoryStore`, and `SkillManager`, and injects the
  actual fork function.
- Runtime: `Reviewer.Observe` is called after cleanly completed turns only.
  Interrupted, cancelled, and max-step turns are ignored.
- Shutdown: app teardown cancels the review context and flips a liveness flag
  so late write notifications do not mutate stale UI state.
- Concurrency: `Reviewer`, `MemoryStore`, and `SkillManager` guard mutable
  state with mutexes. Cross-process disk writes are best-effort via atomic
  rename, not a distributed lock.

## Tests

```
internal/selflearn/reviewer_test.go       — cadence, single-flight behavior,
                                             seeding, and result filtering.
internal/selflearn/concurrency_test.go    — snapshot copying and race safety.
internal/selflearn/fork_test.go           — fork prompt/tool restrictions and
                                             inherited system state.
internal/selflearn/memory_test.go         — memory add/replace/remove, limits,
                                             traversal, and threat scanning.
internal/selflearn/skill_test.go          — skill create/update/delete,
                                             provenance, and permissions.
internal/selflearn/config_test.go         — settings resolution and defaults.
internal/selflearn/fixes_test.go          — regression coverage for security
                                             and patch edge cases.
```

## See Also

- Code: `internal/selflearn/`
- Related packages: [`setting`](setting.md), [`skill`](skill.md), [`reminder`](reminder.md)
- Concepts: [`concepts/harness-channels.md`](../../concepts/harness-channels.md)
- Layer: `feature` (see [`reference/dependency-rules.md`](../../reference/dependency-rules.md))
