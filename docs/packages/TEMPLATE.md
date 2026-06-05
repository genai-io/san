# TEMPLATE for `docs/packages/<pkg>.md`

Copy this file when authoring a new package page. Remove these instructions
and replace placeholders. Keep section order and headings exactly as below.

---

```yaml
---
package: github.com/genai-io/san/internal/<pkg>
layer: <cmd | app | feature | core | infrastructure>
---
```

# <pkg>

One sentence describing what this package owns.

## Purpose

Two to three sentences. Who depends on it, what user-visible behavior it
backs, and the boundary it owns.

## Contract

The public Go interface(s) consumers depend on. **Copy verbatim from
`internal/<pkg>/types.go` (or the file that defines the exported surface).**
Update both files in the same pull request.

```go
package <pkg>

// Engine documents the seam exposed to callers.
type Engine interface {
    Run(ctx context.Context, event Event) (Result, error)
}

type Event struct {
    Name    string
    Payload map[string]any
}

type Result struct {
    Block   bool
    Message string
}
```

### Contract Rules

These rules govern what is allowed in this section. A reviewer should reject
the page if any rule is violated; later a doc linter will enforce them.

1. **Small.** 1–3 methods on any one interface. If you need more, split the
   interface or expose a concrete struct instead.
2. **Defined at the consumer side.** The interface lists only what callers
   need; not what the implementation happens to expose.
3. **No speculative abstractions.** If there is exactly one implementation
   and no second one in sight, **delete the interface and expose the
   concrete type**.
4. **Naming.** Single-method interfaces use the `-er` suffix (`Reader`,
   `Closer`, `Runner`). Multi-method use a domain noun (`Engine`, `Store`,
   `Handler`). Never `IFoo`, `FooInterface`, `FooContract`.
5. **Constructors return concrete types.** `func New(...) *T`, not
   `func New(...) Engine`. Callers narrow to the interface at the seam.
6. **No state on the interface.** Method signatures and the value types
   exchanged across the seam only. State lives in the concrete struct.
7. **No wrapper-only interfaces.** If every call site goes through one
   concrete type, the interface adds noise — remove it.

## Internals

Three to six bullets on the implementation. What components exist (matcher,
executor, store, registry)? Where does state live? Any non-obvious algorithm?

## Lifecycle

When is the object created, who owns it, when does it shut down, is it
goroutine-safe, what is reentrancy behavior?

## Tests

Pointer to the main test file(s) and a one-line summary of what each covers.

```
internal/<pkg>/<pkg>_test.go    — table-driven happy-path tests.
internal/<pkg>/<sub>_test.go    — edge cases for <subcomponent>.
```

## See Also

- Code: `internal/<pkg>/`
- Related packages: [`packages/<other>.md`](<other>.md)
- Concepts: [`concepts/<topic>.md`](../concepts/<topic>.md)
- Reference: [`reference/<fact>.md`](../reference/<fact>.md)
