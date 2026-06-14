---
package: github.com/genai-io/san/internal/cron
layer: feature
---

# cron

Cron expression scheduler. Maintains a list of `Job`s with cron exprs and
prompts; the TUI's main loop calls `Tick()` periodically; due jobs are
returned for the loop to dispatch as agent messages.

## Purpose

The user-visible `/loop` and `/schedule` slash commands persist their
recurring or one-shot jobs here. Durable jobs survive process restart;
non-durable jobs are in-memory only.

## Contract

Cron expression scheduler. Jobs persist to disk when marked durable; the TUI loop calls Tick() to fire due jobs. The package exposes `*Scheduler` directly — no Service interface.

```go
package cron

// Scheduler is the opaque handle. Type exported; fields unexported.
type Scheduler struct { /* internal fields */ }

func (s *Scheduler) Add(job Job) error
func (s *Scheduler) Remove(id string) bool
func (s *Scheduler) Create(cronExpr, prompt string, recurring, durable bool) (*Job, error)
func (s *Scheduler) Delete(id string) error
func (s *Scheduler) List() []*Job
func (s *Scheduler) Tick() []FiredJob
func (s *Scheduler) Empty() bool
func (s *Scheduler) Reset()
func (s *Scheduler) SetStoragePath(path string)
func (s *Scheduler) LoadDurable() error

// Package-level access
func Initialize(opts Options)
func Default() *Scheduler
func SetDefaultScheduler(s *Scheduler)  // test-only
func ResetDefaultScheduler()        // test-only
```


## Internals

- `Scheduler` (`store.go`) — concrete implementation. Holds the job map +
  optional `storagePath` for durable persistence.
- `cron.go` — `Job` struct, cron expression parsing, next-fire-time
  calculation.
- `loop.go` — internal loop used by tests; production code uses the
  TUI's tick instead.

## Lifecycle

- Construction: `Initialize(Options{StoragePath})` loads durable jobs
  from disk.
- Per-tick: the TUI calls `Tick()` once per UI tick; returned `FiredJob`s
  are converted to user messages and routed through the agent.
- Persistence: durable jobs are written to `<storagePath>` on Add/Delete.

## Tests

```
internal/cron/cron_test.go    — expression parsing, next-fire, durability.
internal/cron/loop_test.go    — tick semantics.
```

## See Also

- Code: `internal/cron/`
- Reference: [`reference/slash-commands.md`](../../reference/slash-commands.md) (`/loop`, `/schedule`)
- Layer: `feature`
