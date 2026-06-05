---
package: github.com/genai-io/san/internal/identity
layer: feature
---

# identity

Loads user-defined persona markdown files (`~/.san/identities/*.md`,
`<project>/.san/identities/*.md`) and exposes them through a `Registry`.
The TUI's `/identity` selector lists what's here; the active identity is
written into the system prompt.

## Purpose

An identity is the persona block of the system prompt: tone, expertise,
default workflow conventions. Two scopes — user (`~/.san/identities/`)
and project (`<project>/.san/identities/`, takes precedence) — plus a
built-in `default` identity rendered from `prompts/identity.txt`.

## Contract

This is one of the more idiomatic packages in the repo: it exposes a
**concrete `*Registry` type**, not a god `Service` interface.

```go
package identity

// Registry holds the set of available identities loaded from disk plus
// the virtual default. Concurrent-safe.
type Registry struct {
    // unexported
}

func NewRegistry(cwd string) *Registry
func (r *Registry) Reload()
func (r *Registry) List() []*Identity
func (r *Registry) Get(name string) (*Identity, bool)
// ... plus a handful of small Get/Has accessors

// Identity is one persona definition (frontmatter + body).
type Identity struct {
    Name        string
    Description string
    Body        string
    Path        string
    Scope       Scope
}

func (i Identity) IsBuiltin() bool

// Scope tells where an identity was loaded from.
type Scope int
const (
    ScopeBuiltin Scope = iota
    ScopeUser
    ScopeProject
)

// DefaultName is the reserved name of the virtual built-in identity.
const DefaultName = "default"

func DefaultIdentity() *Identity
func Initialize(cwd string)
func Default() *Registry
```

### Known Violations

Few: this package returns `*Registry` (concrete) from `Default()`, has a
small focused surface, and avoids the kitchen-sink `Service` pattern.
Minor notes:

- **Singleton via `Default()`** still applies; same advice as the rest
  of the codebase (move construction to composition root).
- **`Initialize(cwd)` signature is positional.** Most other packages use
  an `Options` struct — consistency would help.

Use this package as the reference shape for what other `feature` packages
should look like after their PR-3 refactor.

## Internals

- `Registry` (`registry.go`) — in-memory map + alphabetical display
  ordering (default → project → user).
- `identity.go` — `Identity` value type plus markdown frontmatter
  parsing.
- `path.go` — `~/.san/identities/` and `<project>/.san/identities/`
  resolution.
- `template.go` + `README.md.tmpl` — bootstrap a README when the
  user-level directory is created.

## Lifecycle

- Construction: `Initialize(cwd)` runs at app start and on cwd change.
- Reload: `(*Registry).Reload()` rescans the two directories.
- Per-call: `Get(name)` and `List()` are RWMutex-protected reads.

## Tests

```
internal/identity/identity_test.go    — load, frontmatter parsing,
                                         scope ordering.
```

## See Also

- Code: `internal/identity/`
- System prompt: [`packages/core.md`](core.md) (System.Use of an identity section)
- Concepts: [`concepts/harness-channels.md`](../concepts/harness-channels.md)
- Reference: [`reference/slash-commands.md`](../reference/slash-commands.md) (`/identity`)
- Layer: `feature`
