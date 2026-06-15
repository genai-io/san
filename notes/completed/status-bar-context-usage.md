# Status Bar — Real-Time Context Usage

Port of the Hermes-authored PRD at [`../../status-bar-context-usage-prd.md`](../../status-bar-context-usage-prd.md):
a permanent single-line status bar that shows context-window fill as a 10-cell
bar with 4 color tiers, plus a compressions badge. SAN already implements
~80% of the plumbing — this plan covers the rendering layer plus a new
compressions counter.

## Context

Existing pieces in SAN that the PRD's "backend contract" maps onto:

- **Numerator:** `env.InputTokens` (`internal/app/env.go:31`), updated per LLM
  response in `OnTokenUsage` (`internal/app/model_agent_events.go:45`).
- **Denominator:** `kit.GetEffectiveInputLimit()` via `env.CurrentModel`
  (`internal/app/view.go:246`).
- **Cost:** `env.ConversationCost`, accumulated in `OnTokenUsage`.
- **Status line:** `renderModeStatus` → `conv.RenderModeStatus` →
  `renderModelWithTokens` (`internal/app/conv/message.go:76`) already renders
  `{model} · {tokens}/{limit} ({pct}%) · {hint}`.
- **Compact handling:** `OnCompacted` (`internal/app/model_compact.go:39`)
  calls `env.ResetContextDisplay()`.
- **Auto-compact trigger:** `NeedsCompaction`
  (`internal/core/message.go:293`) fires at 95%.
- **Humanized format:** `kit.FormatTokenCount` exists.
- **Width-aware rendering:** `RenderModeStatus` already takes `Width`.

What's missing: the visual bar, 4-tier color (currently 3 at 85/95),
compressions counter and badge, responsive segment allocator.

## Decisions

| Decision | Choice | Reason |
|---|---|---|
| Scope | Full PRD | All four pieces: bar, tiers, badge, responsive allocator |
| Auto-compact trigger | Keep 95% | Conservative; the "critical" tier becomes a brief flash before compression fires |
| Color tiers | 4, composed from existing theme tokens | No new theme infrastructure (`Success` / `Warning` / `Error` / `Error+Bold`) |
| Architecture | New `internal/app/conv/status_bar.go` module | Matches existing convention; isolates new logic for testing |

## Architecture

```
agent LLM response ──► OnTokenUsage               (existing)
                       env.InputTokens = resp.InputTokens
                              │
agent compact event ──► OnCompacted                (existing + 1 new line)
                       env.ResetContextDisplay()
                       env.Compressions++         ← NEW
                              │
                              ▼
view.go:renderModeStatus                          (existing)
   passes OperationModeParams{
       InputTokens, InputLimit, ConversationCost,
       ModelName, Compressions, Width, ...
   }                                              ← add Compressions field
                              │
                              ▼
conv.RenderModeStatus                             (existing orchestrator)
   ├─ left segments (mode, thinking, queue)      ← unchanged
   └─ right segments (NEW status_bar.go)
       ├─ model name
       ├─ ctx X/Y [bar] NN%                      ← NEW bar, 4-tier color
       ├─ 🗜️ N                                    ← NEW compressions badge
       ├─ $cost
       └─ AllocateStatusSegments(width)          ← NEW tail-budget allocator
```

The new file `internal/app/conv/status_bar.go` owns **only what's new**: the
bar, the 4-tier resolver, the badge, and the budget allocator. Existing
helpers stay where they are.

## Components

```go
package conv

// Threshold percentages (PRD §7.2 boundaries).
const (
    contextBarWidth = 10  // PRD §7.1
    pctGood     = 50
    pctWarn     = 80
    pctCritical = 95
)

// contextTier classifies a fill percentage into one of 4 PRD tiers.
// Off-by-one preserved: 80 falls into warn, only >80 is bad. (PRD §7.2)
type contextTier int
const (
    tierNone contextTier = iota  // pct unknown (no denominator)
    tierGood                     // [0, 50]
    tierWarn                     // (50, 80]
    tierBad                      // (80, 95)
    tierCritical                 // [95, 100]
)

func classifyContextTier(pct float64) contextTier        // pure
func (t contextTier) style() lipgloss.Style              // Success / Warning / Error / Error+Bold

// Pure renderers — take primitives, return styled strings.
func RenderContextBar(used, limit int) string            // "[██████░░░░] 71%"
func RenderContextLabel(used, limit int) string          // "ctx 142.0k/200.0k" (via kit.FormatTokenCount)
func RenderCompressionsBadge(n int) string               // "🗜️ 2", "" when n==0
func compressionBadgeStyle(n int) lipgloss.Style         // <5 muted, 5-9 warn, >=10 error

// statusSegment is a unit the allocator can keep or drop.
type statusSegment struct {
    render   func() string  // lazy — only called if segment survives
    width    int            // precomputed visible width
    priority int            // lower = drops first (PRD §8.3)
}

func buildRightSegments(p OperationModeParams) []statusSegment

// AllocateStatusSegments walks segments in priority order, keeping each
// that fits in the remaining budget. Never truncates mid-segment.
func AllocateStatusSegments(segments []statusSegment, availableWidth int) string
```

**PRD requirement → SAN function:**

| PRD § | SAN function |
|---|---|
| §7.1 10-cell bar | `RenderContextBar` |
| §7.2 4-tier color | `classifyContextTier` + `style()` |
| §7.3 `--` / `NN%` label | inside `RenderContextBar` |
| §7.4 `ctx X/Y` | `RenderContextLabel` (uses `kit.FormatTokenCount`) |
| §7.5 `🗜️ N` | `RenderCompressionsBadge` |
| §8.3 priority drop | `AllocateStatusSegments` |

`renderModelWithTokens` in `message.go` shrinks to just
`{model} · {status message}` and delegates context/cost to the new module.

## Data flow & lifecycle boundaries

**New env state** (`internal/app/env.go`):

```go
type env struct {
    ...
    Compressions    int  // NEW — auto + manual compact count this session
    ...
}

func (m *env) ResetContextDisplay() {
    m.InputTokens = 0
    m.OutputTokens = 0
    m.ConversationCost = llm.Money{}
    // NOTE: Compressions intentionally NOT reset here.
    // ResetContextDisplay fires on every compact; the count must survive.
}

func (m *env) ResetTokens() {
    m.ResetContextDisplay()
    m.TurnInputTokens = 0
    m.TurnOutputTokens = 0
    m.turnUsageActive = false
    m.Compressions = 0  // NEW — only place it zeroes (called by /reset, /new)
}
```

**Where Compressions increments** (`internal/app/model_compact.go:39`):

```go
func (m *model) OnCompacted(info core.CompactInfo) tea.Cmd {
    scrollbackCmds := m.commitAllMessages()
    m.conv.Clear()
    m.env.ResetContextDisplay()
    m.env.Compressions++  // NEW — both auto and manual
    ...
}
```

**Plumbing through to renderer** (`internal/app/view.go:243`):

```go
return conv.RenderModeStatus(conv.OperationModeParams{
    ...
    Compressions: m.env.Compressions,  // NEW
    ...
})
```

`OperationModeParams` (`internal/app/conv/message.go:32`) gets a
`Compressions int` field.

**Lifecycle boundaries (PRD §6.2):** SAN re-renders on every `tea.Msg`, so
no new emit logic is needed. The status bar naturally refreshes on:

- `OnTokenUsage` — after every LLM response ✓
- `OnCompacted` — after auto/manual compact ✓ (now with increment)
- `OnTurnEnd` — after every turn boundary ✓
- Model switch via `SwitchProvider` ✓ (updates `CurrentModel` which feeds `InputLimit`)
- `/reset` via `ResetTokens` ✓ (now with Compressions zeroed)

**Sentinel handling (PRD §4.3):** SAN does not use a `-1` sentinel —
`ResetContextDisplay` zeroes the field. `RenderContextBar` still clamps
negatives to 0 defensively as a one-line guard.

## Edge cases (PRD §9)

| Case | Behavior in SAN |
|---|---|
| `InputLimit == 0` (model metadata not loaded yet) | `RenderContextBar` returns `[----------] --`, dim styling |
| First turn, no API response | `env.InputTokens == 0` → bar shows `0%`; label `ctx --/200K` |
| Model switch mid-session | `CurrentModel` updates → `kit.GetEffectiveInputLimit` returns new denominator → next render shows new limit |
| Manual `/compact` | `OnCompacted` fires → `InputTokens=0`, `Compressions++` → bar drops to `0%`, badge appears |
| `/reset` or `/new` | `ResetTokens` zeroes everything including `Compressions` |
| `pct > 100` (provider over-send) | Clamp inside `RenderContextBar`: `pct = min(pct, 100)` |
| Post-compact transitional turn | Bar shows `0%` for one frame until next `OnTokenUsage` |
| Terminal resize | Existing `Width` plumbing handles it; `AllocateStatusSegments` re-evaluates each render |

## Testing

**New file `internal/app/conv/status_bar_test.go`:**

- `TestClassifyContextTier_Boundaries` — verifies PRD §7.2 off-by-one:
  - `0 → good`, `49 → good`, `50 → good`
  - `51 → warn`, `80 → warn` (NOT bad!)
  - `81 → bad`, `94 → bad`
  - `95 → critical`, `100 → critical`
- `TestRenderContextBar_FillLevels` — 0%, 49%, 50%, 80%, 95%, 100%, clamp at 120%.
- `TestRenderContextBar_NoLimit` — returns `--`, dim style.
- `TestRenderCompressionsBadge_Escalation` — `0 → ""`, `1 → dim`, `4 → dim`, `5 → warn`, `9 → warn`, `10 → error`.
- `TestAllocateStatusSegments_DropsInPriorityOrder` — at width 100, 70, 50, 30; verifies cost drops first, then badge, then bar, then label.
- `TestAllocateStatusSegments_NeverTruncatesMidSegment` — segment widths respected.

**Update `internal/app/conv/message_test.go`:**

- `TestRenderModeStatusShowsTokenUsageWithModel` — update expected string to include `[bar] NN%`.
- `TestRenderModeStatusKeepsContextDisplayOnRightOnly` — update expected layout.
- Add `TestRenderModeStatusShowsCompressionsBadgeWhenNonZero`.
- Add `TestRenderModeStatusHidesBadgeWhenZero`.

Existing tests that should pass unchanged: anything touching
`RenderThinkingIndicator`, `RenderOperationModeIndicator`,
`renderQueueBadge`, `RenderTurnUsageSummary`.

## Acceptance criteria (PRD §11)

- [x] After every assistant turn, bar reflects `env.InputTokens / InputLimit`.
- [x] Color flips at exactly 50 / 80 / 95 (80 stays warn, only `>80` is bad).
- [x] No negative percentages ever render.
- [x] Status bar fits on a single row at every column width from 40 to 200.
- [x] At `cols < 52`, bar + percent hide (they're one segment); allocator drops bar/badge/cost in priority order. *(Verified via render-check at width 40: model + label survive; percent travels with the bar.)*
- [x] `/reset` returns the bar to `0%` and clears the compressions badge.
- [x] Switching models updates the denominator within one turn.
- [x] `🗜️ N` appears only after the first compact, never at session start.
- [x] `go test ./internal/app/...` is green. *(Full `go test ./...` green; `go vet` clean; `go build ./cmd/san` clean; gofmt clean.)*

## Files touched

| File | Change |
|---|---|
| `internal/app/conv/status_bar.go` | NEW — bar, tier resolver, badge, allocator |
| `internal/app/conv/status_bar_test.go` | NEW — unit tests |
| `internal/app/conv/message.go` | Slim `renderModelWithTokens`; add `Compressions` to `OperationModeParams` |
| `internal/app/conv/message_test.go` | Update expected strings; add badge tests |
| `internal/app/env.go` | Add `Compressions` field; zero in `ResetTokens` |
| `internal/app/model_compact.go` | Increment `env.Compressions` in `OnCompacted` |
| `internal/app/view.go` | Pass `Compressions` into `OperationModeParams` |
