# Status Bar — Real-Time Context Usage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a 10-cell context-usage bar with 4-tier color, a compressions badge, and a responsive segment allocator to SAN's TUI status line.

**Architecture:** New `internal/app/conv/status_bar.go` module owns the bar, tier resolver, badge, and allocator. Existing plumbing (`env.InputTokens`, `kit.GetEffectiveInputLimit`, `OnCompacted`) stays; we add an `env.Compressions` counter and a `Compressions` field on `OperationModeParams`.

**Tech Stack:** Go 1.x, `charm.land/lipgloss/v2` for styling, standard `testing` package.

**Spec:** [`status-bar-context-usage.md`](status-bar-context-usage.md) · **PRD:** [`../../status-bar-context-usage-prd.md`](../../status-bar-context-usage-prd.md)

---

## File map

| File | Status | Responsibility |
|---|---|---|
| `internal/app/env.go` | Modify | Add `Compressions int` field; zero in `ResetTokens` only |
| `internal/app/env_test.go` (NEW) | Create | Verify counter survives `ResetContextDisplay`, zeroes on `ResetTokens` |
| `internal/app/model_compact.go` | Modify | Increment `env.Compressions` in `OnCompacted` |
| `internal/app/conv/message.go` | Modify | Add `Compressions` to `OperationModeParams`; slim `renderModelWithTokens` to delegate to `status_bar.go` |
| `internal/app/conv/status_bar.go` | Create | Bar, tier resolver, badge, segment allocator |
| `internal/app/conv/status_bar_test.go` | Create | Unit tests for the new module |
| `internal/app/conv/message_test.go` | Modify | Update expected strings; add badge tests |
| `internal/app/view.go` | Modify | Pass `m.env.Compressions` into `OperationModeParams` |

---

## Task 1: Add `Compressions` counter to `env`

**Files:**
- Modify: `internal/app/env.go`
- Create: `internal/app/env_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/app/env_test.go`:

```go
package app

import "testing"

func TestResetContextDisplay_PreservesCompressions(t *testing.T) {
	e := &env{Compressions: 3, InputTokens: 100, OutputTokens: 50}
	e.ResetContextDisplay()
	if e.Compressions != 3 {
		t.Errorf("Compressions = %d, want 3 (must survive ResetContextDisplay)", e.Compressions)
	}
	if e.InputTokens != 0 || e.OutputTokens != 0 {
		t.Errorf("InputTokens=%d OutputTokens=%d, want 0/0", e.InputTokens, e.OutputTokens)
	}
}

func TestResetTokens_ZeroesCompressions(t *testing.T) {
	e := &env{Compressions: 5, TurnInputTokens: 80, TurnOutputTokens: 40}
	e.ResetTokens()
	if e.Compressions != 0 {
		t.Errorf("Compressions = %d, want 0 after ResetTokens", e.Compressions)
	}
	if e.TurnInputTokens != 0 || e.TurnOutputTokens != 0 {
		t.Errorf("turn tokens not zeroed: %d/%d", e.TurnInputTokens, e.TurnOutputTokens)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestResetContextDisplay_PreservesCompressions -v`
Expected: compile error — `env.Compressions` undefined.

- [ ] **Step 3: Add the field and update reset methods**

In `internal/app/env.go`, add the field to the `env` struct (after `ConversationCost` around line 40):

```go
// Compressions counts auto + manual compacts this session. Survives
// ResetContextDisplay (called per-compact); zeroed only by ResetTokens
// (called on /reset, /new).
Compressions int
```

Update `ResetTokens` (around line 238) to zero it:

```go
func (m *env) ResetTokens() {
	m.ResetContextDisplay()
	m.TurnInputTokens = 0
	m.TurnOutputTokens = 0
	m.turnUsageActive = false
	m.Compressions = 0
}
```

Leave `ResetContextDisplay` (around line 232) unchanged — it must NOT zero `Compressions`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/ -run 'TestResetContextDisplay_PreservesCompressions|TestResetTokens_ZeroesCompressions' -v`
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/env.go internal/app/env_test.go
git commit -m "feat(app): add Compressions counter to env

Survives ResetContextDisplay (fires per-compact); zeroed only by
ResetTokens (/reset, /new). Backs the upcoming status-bar badge."
```

---

## Task 2: Increment `Compressions` on every compact

**Files:**
- Modify: `internal/app/model_compact.go:39`
- Modify: `internal/app/env_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/app/env_test.go`:

```go
func TestCompressions_StartsAtZero(t *testing.T) {
	e := &env{}
	if e.Compressions != 0 {
		t.Errorf("Compressions = %d, want 0 at session start", e.Compressions)
	}
}
```

This is a guard against future regressions of the default value. The increment behavior itself is exercised in the manual smoke test (Task 10) since `OnCompacted` requires a fully-wired `model` that's expensive to construct in a unit test.

- [ ] **Step 2: Run test to verify it passes (no impl change needed yet)**

Run: `go test ./internal/app/ -run TestCompressions_StartsAtZero -v`
Expected: PASS (zero-value default).

- [ ] **Step 3: Add the increment line in `OnCompacted`**

In `internal/app/model_compact.go`, inside `OnCompacted` (line 39), add the increment immediately after `m.env.ResetContextDisplay()`:

```go
func (m *model) OnCompacted(info core.CompactInfo) tea.Cmd {
	scrollbackCmds := m.commitAllMessages()

	m.conv.Clear()
	m.env.ResetContextDisplay()
	m.env.Compressions++
	token := m.userInput.Provider.SetStatusMessage("compacted")
	// ... rest unchanged
}
```

- [ ] **Step 4: Build to verify compilation**

Run: `go build ./internal/app/...`
Expected: no errors.

- [ ] **Step 5: Run package tests to verify no regression**

Run: `go test ./internal/app/...`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/model_compact.go internal/app/env_test.go
git commit -m "feat(app): increment Compressions on every compact

OnCompacted fires for both auto and manual compact; bump the counter
there so the upcoming status-bar badge reflects lifetime compactions."
```

---

## Task 3: Add `Compressions` field to `OperationModeParams` and wire through `view.go`

**Files:**
- Modify: `internal/app/conv/message.go:32`
- Modify: `internal/app/view.go:243`

- [ ] **Step 1: Add the field to the params struct**

In `internal/app/conv/message.go`, extend `OperationModeParams` (line 32):

```go
type OperationModeParams struct {
	Mode             setting.OperationMode
	InputTokens      int
	OutputTokens     int
	InputLimit       int
	ModelName        string
	StatusMessage    string
	ConversationCost llm.Money
	Compressions     int  // NEW — session compact count, drives the badge
	Width            int
	ThinkingEffort   string
	ShowThinking     bool
	QueueCount       int
}
```

- [ ] **Step 2: Pass it from the call site**

In `internal/app/view.go`, inside `renderModeStatus` (line 242), add the new field to the literal:

```go
return conv.RenderModeStatus(conv.OperationModeParams{
	Mode:             m.env.OperationMode,
	InputTokens:      m.env.InputTokens,
	OutputTokens:     m.env.OutputTokens,
	InputLimit:       kit.GetEffectiveInputLimit(m.services.LLM.Store(), m.env.CurrentModel),
	ModelName:        modelName,
	StatusMessage:    m.userInput.Provider.StatusMessage,
	ConversationCost: m.env.ConversationCost,
	Compressions:     m.env.Compressions,
	Width:            m.env.Width,
	ThinkingEffort:   thinkingEffort,
	ShowThinking:     showThinking,
	QueueCount:       m.userInput.Queue.Len(),
})
```

- [ ] **Step 3: Build and run existing conv tests**

Run: `go build ./... && go test ./internal/app/conv/...`
Expected: build clean; existing tests PASS (the new field defaults to 0, no behavior change yet).

- [ ] **Step 4: Commit**

```bash
git add internal/app/conv/message.go internal/app/view.go
git commit -m "feat(tui): thread Compressions through OperationModeParams

Plumbing only — no rendering change. The new field reaches the status
bar via the existing renderModeStatus call site."
```

---

## Task 4: Implement `classifyContextTier` and tier styling

**Files:**
- Create: `internal/app/conv/status_bar.go`
- Create: `internal/app/conv/status_bar_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/app/conv/status_bar_test.go`:

```go
package conv

import "testing"

func TestClassifyContextTier_Boundaries(t *testing.T) {
	cases := []struct {
		pct float64
		want contextTier
	}{
		{0, tierGood},      // empty context
		{49, tierGood},     // just under warn
		{50, tierGood},     // PRD §7.2: 50 stays good
		{50.01, tierWarn},  // just past good
		{51, tierWarn},
		{79, tierWarn},
		{80, tierWarn},     // PRD §7.2 off-by-one: 80 stays warn
		{81, tierBad},      // only >80 is bad
		{94, tierBad},
		{94.99, tierBad},
		{95, tierCritical}, // PRD §7.2: ≥95 is critical
		{100, tierCritical},
		{120, tierCritical}, // clamp handled upstream; classifier still critical
	}
	for _, c := range cases {
		got := classifyContextTier(c.pct)
		if got != c.want {
			t.Errorf("classifyContextTier(%.2f) = %v, want %v", c.pct, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/conv/ -run TestClassifyContextTier_Boundaries -v`
Expected: compile error — `classifyContextTier` and the `contextTier` constants undefined.

- [ ] **Step 3: Implement the classifier and constants**

Create `internal/app/conv/status_bar.go`:

```go
// Package conv: status-bar components — context bar, color tiers,
// compressions badge, and the responsive segment allocator. Pure
// functions over primitives; the orchestrator (RenderModeStatus) wires
// them to env state.
package conv

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// Threshold percentages for the 4 PRD §7.2 color tiers.
const (
	pctGood     = 50.0
	pctWarn     = 80.0
	pctCritical = 95.0

	// contextBarWidth is the cell count for the visual bar (PRD §7.1).
	contextBarWidth = 10
)

// contextTier classifies a context-window fill percentage into one of
// the 4 PRD §7.2 tiers. Off-by-one preserved: 80 itself falls into warn,
// only strictly-greater-than-80 is bad. ≥95 is critical.
type contextTier int

const (
	tierNone contextTier = iota // pct unknown — denominator missing
	tierGood                    // [0, 50]    healthy
	tierWarn                    // (50, 80]   watch
	tierBad                     // (80, 95)   pressure
	tierCritical                // [95, 100+] imminent compression
)

// classifyContextTier maps a clamped percentage to its tier. Callers
// must clamp pct to [0, 100] before calling; values >100 still classify
// as critical (defensive).
func classifyContextTier(pct float64) contextTier {
	switch {
	case pct < pctGood:
		return tierGood
	case pct <= pctWarn: // 50 < pct ≤ 80
		return tierWarn
	case pct < pctCritical: // 80 < pct < 95
		return tierBad
	default: // pct ≥ 95
		return tierCritical
	}
}

// style resolves a tier to a lipgloss style composed from existing
// theme tokens (per project decision: no new theme infrastructure).
func (t contextTier) style() lipgloss.Style {
	switch t {
	case tierGood:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
	case tierWarn:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
	case tierBad:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
	case tierCritical:
		// Critical = Error + Bold. Distinct from "bad" without adding a
		// new theme token.
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Bold(true)
	default: // tierNone
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	}
}

// silence unused-import errors until later tasks add callers of fmt/strings.
var _, _ = fmt.Sprintf, strings.Repeat
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/conv/ -run TestClassifyContextTier_Boundaries -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/conv/status_bar.go internal/app/conv/status_bar_test.go
git commit -m "feat(tui): add 4-tier context classifier

PRD §7.2 boundaries (50/80/95) with the exact off-by-one: 80 stays warn,
only >80 is bad. Styles compose from existing theme tokens."
```

---

## Task 5: Implement `RenderContextBar`

**Files:**
- Modify: `internal/app/conv/status_bar.go`
- Modify: `internal/app/conv/status_bar_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/app/conv/status_bar_test.go`:

```go
func TestRenderContextBar_FillLevels(t *testing.T) {
	cases := []struct {
		name     string
		used     int
		limit    int
		wantFill int // expected number of '█' characters
		wantPct  string
	}{
		{"empty", 0, 200000, 0, "0%"},
		{"half", 100000, 200000, 5, "50%"},
		{"near-full", 190000, 200000, 10, "95%"}, // 95% rounds to 10 cells (9.5 → 10)
		{"full", 200000, 200000, 10, "100%"},
		{"oversend-clamped", 240000, 200000, 10, "100%"}, // pct > 100 clamped
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderContextBar(c.used, c.limit)
			if strings.Count(got, "█") != c.wantFill {
				t.Errorf("fill cells = %d, want %d (rendered: %q)", strings.Count(got, "█"), c.wantFill, got)
			}
			if !strings.Contains(got, c.wantPct) {
				t.Errorf("pct label %q missing from %q", c.wantPct, got)
			}
		})
	}
}

func TestRenderContextBar_NoLimit(t *testing.T) {
	got := RenderContextBar(100, 0)
	if !strings.Contains(got, "--") {
		t.Errorf("missing '--' label for unknown limit; got %q", got)
	}
	// No fill cells when denominator is unknown.
	if strings.Contains(got, "█") {
		t.Errorf("should not render fill cells for unknown limit; got %q", got)
	}
}

func TestRenderContextBar_NoNegativePercent(t *testing.T) {
	// Defensive: callers should pass non-negative, but the renderer
	// must never emit a negative percentage.
	got := RenderContextBar(-1, 200000)
	if strings.Contains(got, "-") {
		t.Errorf("negative percent rendered: %q", got)
	}
}
```

(Also add `"strings"` to the import block at the top of the test file.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app/conv/ -run 'TestRenderContextBar_' -v`
Expected: compile error — `RenderContextBar` undefined.

- [ ] **Step 3: Implement `RenderContextBar`**

In `internal/app/conv/status_bar.go`, remove the `var _, _ = fmt.Sprintf, strings.Repeat` placeholder line and add:

```go
// RenderContextBar renders the 10-cell bar with a percentage label:
//
//	"[██████░░░░] 71%"   normal case
//	"[----------] --"    when limit is 0 (unknown)
//
// The percentage is rounded to an integer at this layer (PRD §4.2); the
// engine itself never rounds. `used` is clamped to [0, limit] before
// computing pct so callers cannot accidentally render negatives or >100%.
func RenderContextBar(used, limit int) string {
	if limit <= 0 {
		dim := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
		return dim.Render("[" + strings.Repeat("-", contextBarWidth) + "] --")
	}
	if used < 0 {
		used = 0
	}
	pct := float64(used) / float64(limit) * 100
	if pct > 100 {
		pct = 100
	}
	filled := int((pct / 100) * float64(contextBarWidth) + 0.5) // round to nearest cell
	if filled > contextBarWidth {
		filled = contextBarWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", contextBarWidth-filled)
	style := classifyContextTier(pct).style()
	return style.Render(fmt.Sprintf("[%s] %d%%", bar, int(pct+0.5)))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/conv/ -run 'TestRenderContextBar_' -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/conv/status_bar.go internal/app/conv/status_bar_test.go
git commit -m "feat(tui): render 10-cell context bar

PRD §7.1: █/░ cells inside brackets with integer percent label. Color
tier applied via classifyContextTier. Unknown limit renders '--' dim."
```

---

## Task 6: Implement `RenderContextLabel`

**Files:**
- Modify: `internal/app/conv/status_bar.go`
- Modify: `internal/app/conv/status_bar_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/app/conv/status_bar_test.go`:

```go
func TestRenderContextLabel(t *testing.T) {
	cases := []struct {
		name  string
		used  int
		limit int
		want  string
	}{
		{"compact", 142_000, 200_000, "ctx 142K/200K"},
		{"millions", 1_500_000, 2_000_000, "ctx 1.5M/2M"},
		{"unknown-limit", 5000, 0, "ctx --/200K"}, // used shown, limit dim
		{"zero-used", 0, 200_000, "ctx 0/200K"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderContextLabel(c.used, c.limit)
			visible := stripANSI(got)
			if visible != c.want {
				t.Errorf("RenderContextLabel(%d, %d) visible = %q, want %q (raw: %q)", c.used, c.limit, visible, c.want, got)
			}
		})
	}
}
```

Add the ANSI-stripping helper to the test file (Lipgloss sets color codes that interfere with substring checks):

```go
// stripANSI removes ANSI escape sequences so tests can compare visible text.
var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRegexp.ReplaceAllString(s, "")
}
```

Add `"regexp"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/conv/ -run TestRenderContextLabel -v`
Expected: compile error — `RenderContextLabel` and `stripANSI` undefined.

- [ ] **Step 3: Implement `RenderContextLabel`**

In `internal/app/conv/status_bar.go`, add:

```go
// RenderContextLabel renders the "ctx X/Y" segment using compact
// humanized numbers (PRD §7.4). Limit renders as "--" when unknown.
func RenderContextLabel(used, limit int) string {
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	if limit <= 0 {
		return muted.Render(fmt.Sprintf("ctx %s/--", kit.FormatTokenCount(used)))
	}
	return muted.Render(fmt.Sprintf("ctx %s/%s", kit.FormatTokenCount(used), kit.FormatTokenCount(limit)))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/conv/ -run TestRenderContextLabel -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/conv/status_bar.go internal/app/conv/status_bar_test.go
git commit -m "feat(tui): render ctx X/Y label segment

Compact humanized format via kit.FormatTokenCount. Renders 'ctx X/--'
when the model's context length is unknown."
```

---

## Task 7: Implement `RenderCompressionsBadge` and `compressionBadgeStyle`

**Files:**
- Modify: `internal/app/conv/status_bar.go`
- Modify: `internal/app/conv/status_bar_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/app/conv/status_bar_test.go`:

```go
func TestRenderCompressionsBadge(t *testing.T) {
	cases := []struct {
		name    string
		n       int
		visible string // empty means badge should not render
	}{
		{"zero-hidden", 0, ""},
		{"one", 1, "🗜️ 1"},
		{"four-dim", 4, "🗜️ 4"},
		{"five-warn", 5, "🗜️ 5"},
		{"nine-warn", 9, "🗜️ 9"},
		{"ten-error", 10, "🗜️ 10"},
		{"twenty-error", 20, "🗜️ 20"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderCompressionsBadge(c.n)
			visible := stripANSI(got)
			if c.visible == "" {
				if got != "" {
					t.Errorf("RenderCompressionsBadge(%d) = %q, want empty", c.n, got)
				}
				return
			}
			if visible != c.visible {
				t.Errorf("RenderCompressionsBadge(%d) visible = %q, want %q", c.n, visible, c.visible)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/conv/ -run TestRenderCompressionsBadge -v`
Expected: compile error — `RenderCompressionsBadge` undefined.

- [ ] **Step 3: Implement the badge**

In `internal/app/conv/status_bar.go`, add:

```go
// compressionBadgeStyle escalates color with count (PRD §7.5):
//
//	<5     muted
//	5–9    warn
//	≥10    error
func compressionBadgeStyle(n int) lipgloss.Style {
	switch {
	case n >= 10:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
	case n >= 5:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
	default:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	}
}

// RenderCompressionsBadge returns the "🗜️ N" badge or "" when n ≤ 0.
// Color escalates per compressionBadgeStyle.
func RenderCompressionsBadge(n int) string {
	if n <= 0 {
		return ""
	}
	return compressionBadgeStyle(n).Render(fmt.Sprintf("🗜️ %d", n))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/conv/ -run TestRenderCompressionsBadge -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/conv/status_bar.go internal/app/conv/status_bar_test.go
git commit -m "feat(tui): add compressions badge

PRD §7.5: '🗜️ N' shown only when N>0, with color escalation at 5 and 10."
```

---

## Task 8: Implement `statusSegment` and `AllocateStatusSegments`

**Files:**
- Modify: `internal/app/conv/status_bar.go`
- Modify: `internal/app/conv/status_bar_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/app/conv/status_bar_test.go`:

```go
func TestAllocateStatusSegments_DropsInPriorityOrder(t *testing.T) {
	// Priorities: 1=highest (drops last), 4=lowest (drops first).
	segments := []statusSegment{
		{render: func() string { return "[bar] 71%" }, width: 11, priority: 1},
		{render: func() string { return "ctx 142K/200K" }, width: 14, priority: 2},
		{render: func() string { return "🗜️ 2" }, width: 5, priority: 3},
		{render: func() string { return "$0.04" }, width: 5, priority: 4},
	}

	// Wide: everything fits with separators.
	got := AllocateStatusSegments(segments, 100)
	for _, s := range segments {
		if !strings.Contains(got, stripANSI(s.render())) {
			t.Errorf("wide: segment %q dropped from %q", stripANSI(s.render()), got)
		}
	}

	// Narrow (~30 cols): lowest-priority segments drop first.
	got = AllocateStatusSegments(segments, 30)
	if !strings.Contains(got, "[bar] 71%") {
		t.Errorf("priority-1 segment should survive at width 30; got %q", got)
	}
	if strings.Contains(got, "$0.04") {
		t.Errorf("priority-4 (cost) should drop first at width 30; got %q", got)
	}
}

func TestAllocateStatusSegments_NeverTruncatesMidSegment(t *testing.T) {
	segments := []statusSegment{
		{render: func() string { return "exactly12ch" }, width: 11, priority: 1},
	}
	// Just enough room.
	got := AllocateStatusSegments(segments, 11)
	if !strings.Contains(got, "exactly12ch") {
		t.Errorf("segment should fit at width=its own width; got %q", got)
	}
	// One col too narrow.
	got = AllocateStatusSegments(segments, 10)
	if strings.Contains(got, "exactly12") {
		t.Errorf("segment must drop, not truncate, when it doesn't fit; got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app/conv/ -run 'TestAllocateStatusSegments_' -v`
Expected: compile error — `statusSegment` and `AllocateStatusSegments` undefined.

- [ ] **Step 3: Implement the type and allocator**

In `internal/app/conv/status_bar.go`, add:

```go
// segmentSep is the visible separator inserted between surviving segments.
const segmentSep = "  " // two spaces — wider than a single space to read as a gap

// statusSegment is a unit the allocator can keep or drop atomically.
// Lower priority drops first when width is constrained (PRD §8.3).
type statusSegment struct {
	render   func() string // lazy — only invoked if the segment survives
	width    int           // precomputed visible width (excluding separators)
	priority int           // 1 = highest (drops last); larger = drops first
}

// AllocateStatusSegments walks segments in priority order, keeping each
// segment that fits in the remaining budget plus its leading separator.
// Segments are never truncated mid-render. Returns the joined string.
//
// "Fits" means: width(remaining segments in priority order, including
// separators between them) ≤ availableWidth. We walk greedily from
// highest priority to lowest, subtracting each accepted segment's width
// (and a separator) from the budget.
func AllocateStatusSegments(segments []statusSegment, availableWidth int) string {
	if availableWidth <= 0 || len(segments) == 0 {
		return ""
	}
	// Sort by priority ascending (1 first). Stable so equal-priority
	// segments keep their original order.
	order := make([]int, len(segments))
	for i := range order {
		order[i] = i
	}
	// Insertion sort — n is tiny (≤6 segments in practice).
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && segments[order[j-1]].priority > segments[order[j]].priority {
			order[j-1], order[j] = order[j], order[j-1]
		}
	}

	type kept struct {
		idx    int
		render string
		width  int
	}
	var survivors []kept
	budget := availableWidth
	for _, idx := range order {
		s := segments[idx]
		need := s.width
		if len(survivors) > 0 {
			need += len(segmentSep)
		}
		if need > budget {
			continue
		}
		survivors = append(survivors, kept{idx: idx, render: s.render(), width: s.width})
		budget -= need
	}

	// Re-emit in the original (caller-supplied) order so the layout reads
	// naturally regardless of priority.
	byIdx := make(map[int]int, len(survivors))
	for i, k := range survivors {
		byIdx[k.idx] = i
	}
	out := make([]string, 0, len(survivors))
	for origIdx := range segments {
		if i, ok := byIdx[origIdx]; ok {
			out = append(out, survivors[i].render)
		}
	}
	return strings.Join(out, segmentSep)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/conv/ -run 'TestAllocateStatusSegments_' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/conv/status_bar.go internal/app/conv/status_bar_test.go
git commit -m "feat(tui): add tail-budget segment allocator

PRD §8.3: walks segments in priority order, drops lowest-priority first
when width-constrained. Never truncates mid-segment. Reassembles in
caller-supplied order so layout reads naturally."
```

---

## Task 9: Wire `status_bar.go` into `RenderModeStatus`

**Files:**
- Modify: `internal/app/conv/message.go` (lines 47–111, 23–24)
- Modify: `internal/app/conv/message_test.go`

- [ ] **Step 1: Update existing tests to expect the new layout**

In `internal/app/conv/message_test.go`, find `TestRenderModeStatusShowsTokenUsageWithModel` (around line 92) and update the expected substring. The right-side cluster now contains the bar instead of `(NN%)`:

```go
func TestRenderModeStatusShowsTokenUsageWithModel(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:   "claude-sonnet-4-6",
		InputTokens: 142000,
		InputLimit:  200000,
		Width:       120,
	})
	visible := stripANSI(rendered)
	if !strings.Contains(visible, "claude-sonnet-4-6") {
		t.Fatalf("RenderModeStatus() = %q, want model name", visible)
	}
	if !strings.Contains(visible, "[") || !strings.Contains(visible, "] 71%") {
		t.Fatalf("RenderModeStatus() = %q, want bar with percent", visible)
	}
	// Old per-call percent format must NOT appear anymore.
	if strings.Contains(visible, "(71%)") {
		t.Fatalf("RenderModeStatus() = %q, should not contain old (NN%%) format", visible)
	}
}
```

Find `TestRenderModeStatusKeepsContextDisplayOnRightOnly` (around line 117) and update it to verify the bar appears once (not the old bare percent):

```go
func TestRenderModeStatusKeepsContextDisplayOnRightOnly(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:   "claude-sonnet-4-6",
		InputTokens: 142000,
		InputLimit:  200000,
		Width:       120,
	})
	visible := stripANSI(rendered)
	if !strings.Contains(visible, "claude-sonnet-4-6") {
		t.Fatalf("want model name in %q", visible)
	}
	// Bar should appear exactly once.
	if strings.Count(visible, "] ") != 1 && strings.Count(visible, "%") != 1 {
		t.Fatalf("want unified context display (single bar + percent) in %q", visible)
	}
	if !strings.Contains(visible, "compact at") && !strings.Contains(visible, "auto-compact") {
		t.Fatalf("want auto-compact hint in %q", visible)
	}
}
```

(If these tests already use a different helper name to strip ANSI, use that. If not, add the same `stripANSI`/`ansiRegexp` helper to `message_test.go` or refactor it into a shared `testutil_test.go`.)

Add two new tests:

```go
func TestRenderModeStatusShowsCompressionsBadgeWhenNonZero(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:    "claude-sonnet-4-6",
		InputTokens:  1000,
		InputLimit:   200000,
		Compressions: 3,
		Width:        120,
	})
	visible := stripANSI(rendered)
	if !strings.Contains(visible, "🗜️ 3") {
		t.Fatalf("want '🗜️ 3' badge in %q", visible)
	}
}

func TestRenderModeStatusHidesBadgeWhenZero(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:    "claude-sonnet-4-6",
		InputTokens:  1000,
		InputLimit:   200000,
		Compressions: 0,
		Width:        120,
	})
	visible := stripANSI(rendered)
	if strings.Contains(visible, "🗜️") {
		t.Fatalf("badge should be hidden when zero; got %q", visible)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app/conv/ -run 'TestRenderModeStatus' -v`
Expected: FAIL — old `(NN%)` format still rendered, badge missing.

- [ ] **Step 3: Replace `renderModelWithTokens` with a segment-based orchestrator**

In `internal/app/conv/message.go`:

1. Update the constant block (lines 19–29) — drop the old `autoCompactThreshold` and rely on `pctCritical` from `status_bar.go`:

```go
const (
	// minWrapWidth is the minimum markdown wrap width.
	minWrapWidth = 40

	// agentContentIndent is the extra indent for agent prompt/response content
	// beyond toolResultExpandedStyle's PaddingLeft(4). Total indent = 4 + 4 = 8 chars.
	agentContentIndent = "    "
)
```

2. Replace the body of `renderModelWithTokens` (lines 76–111) with a thin delegate:

```go
// renderModelWithTokens composes the right-side cluster of the status
// bar: model name, context label, bar, optional compressions badge,
// optional cost. The actual rendering lives in status_bar.go; this
// function preserves the existing call shape from RenderModeStatus.
func renderModelWithTokens(
	modelName, statusMessage string,
	inputTokens, inputLimit int,
	conversationCost llm.Money,
	compressions int,
	width int,
) string {
	if modelName == "" {
		return ""
	}
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	sep := muted.Render(" · ")

	// Pre-render once; lipgloss.Width strips ANSI automatically so we
	// can measure the styled output directly without a helper.
	labelText := ""
	barText := ""
	if inputLimit > 0 {
		labelText = RenderContextLabel(inputTokens, inputLimit)
		barText = RenderContextBar(inputTokens, inputLimit)
		if hint := compactStatusHint(float64(inputTokens) / float64(inputLimit) * 100); hint != "" {
			barText += sep + muted.Render(hint)
		}
	}
	badgeText := RenderCompressionsBadge(compressions)
	costText := ""
	if !conversationCost.IsZero() {
		costText = muted.Render(kit.FormatMoney(conversationCost))
	}

	segments := []statusSegment{
		{render: func() string { return muted.Render(modelName) }, width: lipgloss.Width(modelName), priority: 1},
	}
	if statusMessage != "" {
		segments = append(segments, statusSegment{
			render:   func() string { return muted.Render(statusMessage) },
			width:    lipgloss.Width(statusMessage),
			priority: 2,
		})
	}
	if labelText != "" {
		segments = append(segments, statusSegment{
			render:   func() string { return labelText },
			width:    lipgloss.Width(labelText),
			priority: 3,
		})
		segments = append(segments, statusSegment{
			render:   func() string { return barText },
			width:    lipgloss.Width(barText),
			priority: 4,
		})
	}
	if badgeText != "" {
		segments = append(segments, statusSegment{
			render:   func() string { return badgeText },
			width:    lipgloss.Width(badgeText),
			priority: 5,
		})
	}
	if costText != "" {
		segments = append(segments, statusSegment{
			render:   func() string { return costText },
			width:    lipgloss.Width(costText),
			priority: 6,
		})
	}

	// AllocateStatusSegments joins survivors with its internal two-space
	// separator; we swap that for " · " to match the existing aesthetic.
	allocated := AllocateStatusSegments(segments, width)
	parts := strings.Split(allocated, segmentSep)
	return strings.Join(parts, sep)
}
```

(No `stripStylingForWidth` helper or new import needed — `lipgloss.Width` strips ANSI codes itself, so we measure the styled output directly.)

3. Update the call site in `RenderModeStatus` (line 66) to pass the new args:

```go
right := renderModelWithTokens(
	params.ModelName, params.StatusMessage,
	params.InputTokens, params.InputLimit,
	params.ConversationCost,
	params.Compressions,
	params.Width,
)
```

4. Update `RenderTokenWarning` (lines 182–204) to use `pctCritical` and `pctWarn` instead of the old `autoCompactThreshold`:

```go
func RenderTokenWarning(inputTokens, inputLimit int, compactSuppressed bool) string {
	if inputLimit == 0 || inputTokens == 0 || compactSuppressed {
		return ""
	}
	percent := float64(inputTokens) / float64(inputLimit) * 100
	if percent < pctWarn {
		return ""
	}
	untilCompact := max(int(pctCritical-percent), 0)
	if percent >= pctCritical {
		style := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
		return "  " + style.Render(fmt.Sprintf("⚠ Context nearly full (%d%% used) — auto-compact imminent", int(percent)))
	}
	if percent > pctWarn {
		style := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
		return "  " + style.Render(fmt.Sprintf("⚡ %d%% until auto-compact", untilCompact))
	}
	return ""
}
```

(Note: this preserves the off-by-one — exactly 80 no longer triggers the warning, only `>80` does.)

5. Update `compactStatusHint` (lines 129–138):

```go
func compactStatusHint(percent float64) string {
	switch {
	case percent >= pctCritical:
		return "auto-compact"
	case percent > pctWarn:
		return fmt.Sprintf("compact at %d%%", int(pctCritical))
	default:
		return ""
	}
}
```

- [ ] **Step 4: Run all conv tests**

Run: `go test ./internal/app/conv/... -v`
Expected: all PASS, including updated `TestRenderModeStatus*` tests and the two new badge tests.

- [ ] **Step 5: Run the full app test suite**

Run: `go test ./internal/app/...`
Expected: all PASS.

- [ ] **Step 6: Build the whole project**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/app/conv/message.go internal/app/conv/message_test.go internal/app/conv/status_bar.go
git commit -m "feat(tui): wire status_bar module into RenderModeStatus

renderModelWithTokens now composes a segment list and routes through
AllocateStatusSegments, yielding [bar] NN% + ctx X/Y + compressions
badge + cost. Drops the local autoCompactThreshold constant in favor
of pctCritical/pctWarn from status_bar.go."
```

---

## Task 10: Acceptance verification

**Files:**
- Modify: `notes/active/status-bar-context-usage.md` (acceptance checkboxes)

- [ ] **Step 1: Run the entire test suite**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 2: Run vet and gofmt**

Run: `go vet ./... && gofmt -l internal/app/`
Expected: vet clean; gofmt lists no files (empty output).

If gofmt lists files, run `gofmt -w <file>` and amend the relevant commit (or new "style: gofmt" commit).

- [ ] **Step 3: Build the binary**

Run: `go build -o /tmp/san-statusbar ./cmd/san`
Expected: clean build.

- [ ] **Step 4: Manual smoke at three widths**

Run the binary against a real model in three terminal sizes:

1. **Wide (≥96 cols):**
   - Send a message, wait for response.
   - Confirm status bar shows: `{mode}    {model}  ctx X/Y [██████░░░░] NN%  🗜️ N  $cost`
   - After `/compact`, confirm `🗜️ 1` appears and bar resets to `0%`.

2. **Medium (~80 cols):**
   - Resize terminal.
   - Confirm cost and badge drop first; bar and percent remain.

3. **Narrow (~40 cols):**
   - Confirm bar and label hide; bare `NN%` shows next to model.

- [ ] **Step 5: Verify acceptance criteria**

In `notes/active/status-bar-context-usage.md`, tick each box in the "Acceptance criteria" section that the smoke test confirmed:

- [x] After every assistant turn, bar reflects `env.InputTokens / InputLimit`.
- [x] Color flips at exactly 50 / 80 / 95 (80 stays warn, only `>80` is bad).
- [x] No negative percentages ever render.
- [x] Status bar fits on a single row at every column width from 40 to 200.
- [x] At `cols < 52`, bar hides and only `NN%` shows.
- [x] `/reset` returns the bar to `0%` and clears the compressions badge.
- [x] Switching models updates the denominator within one turn.
- [x] `🗜️ N` appears only after the first compact, never at session start.
- [x] `go test ./internal/app/...` is green.

- [ ] **Step 6: Move plan to completed**

Once all criteria pass, move the plan and spec from `notes/active/` to `notes/completed/`:

```bash
git mv notes/active/status-bar-context-usage.md notes/completed/
git mv notes/active/status-bar-context-usage-plan.md notes/completed/
```

- [ ] **Step 7: Final commit**

```bash
git add notes/active/status-bar-context-usage.md notes/completed/status-bar-context-usage.md notes/completed/status-bar-context-usage-plan.md
git commit -m "docs: mark status-bar feature complete

Acceptance criteria verified; moves plan and spec from notes/active/ to
notes/completed/ per repo convention."
```

---

## Self-review

**Spec coverage:**

| Spec section | Covered by task(s) |
|---|---|
| §4 Components — `classifyContextTier` | Task 4 |
| §4 Components — `RenderContextBar` | Task 5 |
| §4 Components — `RenderContextLabel` | Task 6 |
| §4 Components — `RenderCompressionsBadge` | Task 7 |
| §4 Components — `AllocateStatusSegments` | Task 8 |
| §5 `env.Compressions` field + reset semantics | Task 1 |
| §5 Increment on `OnCompacted` | Task 2 |
| §5 `OperationModeParams` plumbing | Task 3 |
| §5 Lifecycle boundaries (re-render on every tea.Msg) | Already exists; verified in Task 10 |
| §6 Edge cases (no-limit, oversend clamp, post-compact, etc.) | Tasks 5, 7, 10 |
| §7 Testing | Tasks 1–8 (TDD inline) + Task 10 smoke |
| §8 Acceptance criteria | Task 10 |
| §9 Files touched | All seven files appear in tasks above |

No spec gaps.

**Placeholder scan:** No "TBD", "TODO", "implement later", or unspecified code blocks. Every code step shows the actual code; every test step shows the actual test.

**Type consistency:**
- `contextTier` enum defined in Task 4, used in Tasks 4 and 5.
- `statusSegment` struct defined in Task 8, used in Task 9.
- Function names match across tasks: `RenderContextBar`, `RenderContextLabel`, `RenderCompressionsBadge`, `AllocateStatusSegments`, `classifyContextTier`, `compressionBadgeStyle`.
- `pctGood` / `pctWarn` / `pctCritical` constants defined in Task 4, used in Tasks 5, 7, 9.
- `segmentSep` defined in Task 8, referenced in Task 9.

No naming drift.
