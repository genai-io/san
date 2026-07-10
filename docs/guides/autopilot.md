# Autopilot

## Overview

Autopilot is San's autonomy system, designed to minimize human intervention: a
copilot model cruises the session, keeping routine work moving and handing
control back only when something genuinely needs you. It acts through a set of
independently enabled **steers** — proposing the next step, approving gray-zone
tool calls, answering a command's interactive prompts, answering
`AskUserQuestion`, and continuing finished turns toward a mission. Only
gray-zone permission judging is on by default.

Enter Autopilot mode with `shift+tab` (cycle until the amber
`⏵⏵ autopilot on`), and configure it with the `/autopilot` panel. To launch a
mission hands-free, hit the panel's **Start** button — it engages Autopilot and
submits the opening step in one action (see [Start](#start-the-mission)). A
resumed session (`san -r <id>`) comes back in the mode it was saved in.

## The six steers

Steers are à-la-carte toggles, ordered by increasing autonomy. None fire unless
Autopilot mode is engaged.

| Steer | Default | What it does |
|---|---|---|
| **Suggest** | off | Shows a next-step suggestion in the input box. When a mission is set, the suggestion follows the mission; otherwise it uses the generic input prediction. `tab` accepts the suggestion, and `enter` sends it. Suggest only fills the hint text and never submits on its own; when off, this hint is hidden. |
| **Permission** | **on** | Auto-approves gray-zone tool calls the static rules couldn't resolve, judging reversibility, blast radius, and data exfiltration. Fails closed: any error escalates to you. |
| **Bash** | off | Answers an already-approved command's interactive prompt (`Continue? [Y/n]`) when the answer just continues the approved action; skips anything that would widen scope. |
| **Skill** | off | Approves the copilot's skill loads outright, without the judge — a deliberate "trust skills" toggle, separate from Permission because the judge tends to escalate a skill load (it can run scripts). Off ⇒ skill loads fall to the Permission judge (or you). |
| **Question** | off | Answers `AskUserQuestion` for you when the mission makes the choice clear and low-risk; defers to you otherwise. Option labels are validated verbatim — a partial or invented answer becomes a defer. |
| **End** | off | After a finished turn, decides whether to continue toward the mission and types the next instruction itself. Bounded by **Continue at most N times** (default 20); the counter resets on every human turn. |

## Mission

The mission is what the copilot drives toward this session — written in the
`/autopilot` panel's Mission dialog, a small editor: the text you type is the
mission (`enter` saves it, `alt+enter` for a newline; paste works), `ctrl+r` asks
the copilot to refine the draft in place, `ctrl+c` clears it, and `esc` saves and
leaves. Every steer reads it: the steering steers (Suggest, Question, End) drive
toward it, and the safety steers (Permission, Bash) take it as intent context — a
tool call or prompt that plainly advances the mission reads as expected, routine
work. Intent never overrides safety, though: they still escalate anything
irreversible, destructive, out-of-project, or data-leaking, mission or not.

When the End steer decides the mission is **fully accomplished**, it retires
it: the mission is cleared and the steers reset to the passive baseline
(Permission + Bash) — Autopilot stays on, you take the wheel back with the
auto-approve safety net intact.

## Start the mission

The panel's bottom row is two buttons — **Save** and **Start** (`←`/`→` to
pick, `enter` to run):

- **Save** applies the config to the live session and writes it to
  `settings.json` as the default seed, without changing the mode. Use it when
  you're only tuning steers, or want to engage later with `shift+tab`.
- **Start** does everything Save does, then engages Autopilot and kicks the
  mission hands-free: it derives the opening step from the mission and submits
  it itself, so briefing a mission and hitting Start is the whole launch. Start
  needs a mission — with none set it nudges you instead of engaging.

Landing on Autopilot via `shift+tab` no longer auto-starts; it only surfaces the
Suggest steer's proposal (if on). Kicking the mission is always the explicit
Start button.

## Demo: a hands-free scaffold

A two-minute run that exercises the full loop — mission kick-off, gray-zone
approval, auto-continuation, and completion — without touching anything outside
a scratch directory.

**1. Start San in an empty directory:**

```bash
mkdir /tmp/autopilot-demo && cd /tmp/autopilot-demo && san
```

**2. Configure the copilot** — run `/autopilot`:

- Toggle **End** on (Permission is already on).
- Open **Mission** and brief it:

  > Scaffold a `notes/` directory: `todo.md` with a 3-item checklist, `done.md`
  > empty, and `README.md` explaining the layout. Work one file per turn. When
  > all three exist, verify with `ls notes/` — then the mission is complete.

- `esc` back.

**3. Engage** — on the bottom row, press `→` to focus **Start** and hit
`enter`. That's the last key you need to press: Start engages Autopilot and,
with a mission set, derives the opening step and submits it itself.

**4. Watch the run.** Expect a transcript like:

```
❭ Create notes/todo.md with a 3-item checklist.
  ⎿  autopilot · 1/20
● Write(notes/todo.md)
  ⎿  Write → 5 lines
❭ Create an empty notes/done.md.
  ⎿  autopilot · 2/20
...
● Bash(ls notes/)
  ↳ auto-approved · read-only directory listing
  ⎿  Bash → 3 lines
  ✓ autopilot · mission complete
```

Every `❭` in the run carries the green `⎿ autopilot` mark — the copilot typed
them all, opening step included; you never touched the composer. The `ls` is a
gray-zone call the Permission steer approved inline. On `✓ mission complete` the mission is cleared and the steers drop back
to the passive baseline — open `/autopilot` to confirm — while Autopilot stays
engaged.

To see the gentler end of the spectrum, rerun with only **Suggest** on and
engage with `shift+tab`: the copilot proposes each step as ghost text in the
composer and you accept with `tab` + `enter`.

## Reading the transcript

| Mark | Meaning |
|---|---|
| green `⎿ autopilot · 2/5` | the `❭` line above was typed by the copilot (continuation 2 of 5) |
| green `↳ auto-approved · <reason>` | the permission judge let the tool call above through |
| amber `↳ escalated · <reason>` | the judge sent the call back to you |
| green `⏵ autopilot · answered for you` | the copilot answered an `AskUserQuestion` |
| amber `↩ autopilot · this question is yours` | it deferred the question to you |
| amber `↩ autopilot · over to you` | it stopped and handed control back (a decide error rides after it) |
| green `✓ autopilot · mission complete` | the mission is done and retired |

While a decision is in flight the mode line reads `⏵⏵ autopilot · thinking…`;
approvals tally there too (`· 3 approved · 1 escalated`).

## Configuration

The panel edits the live session config. The model, steers, and continuation cap
are saved to `settings.json` as the default for new sessions. The **Steering
Prompt** and **mission** are per-session: they ride the transcript and restore on
`/resume`, but are never written as the default — a new session starts from the
built-in steering instructions with no mission. To carry custom steering
instructions or a mission to another session, export them as a preset and import
the preset there.

The Steering Prompt controls how the copilot drives; it does not replace the
immutable control-plane policy. Every LLM steer always receives that policy,
which fixes the trust boundaries, fail-closed behavior, task-specific safety
rules, and output contract. The existing `systemPrompt` / `systemPromptFile`
configuration keys are retained for compatibility and supply only the editable
steering-instructions portion.

```jsonc
{
  "autoPilot": {
    "model": "anthropic/claude-haiku-4-5", // steer decisions; empty = session model
    "systemPrompt": "…",                   // Steering Prompt; per-session, not written here by the panel
    "systemPromptFile": "~/prompts/pilot.md", // persistent steering default; used when systemPrompt is empty
    "mission": "…",                        // per-session; set via the panel
    "maxContinuations": 20,
    "steers": {
      "suggest": true,
      "permission": true,  // omit for the default (on); false escalates everything
      "bashPrompt": true,  // the Bash steer
      "skill": true,       // the Skill steer — trust skill loads
      "question": true,
      "turnEnd": true      // the End steer
    }
  }
}
```

Named presets bundle the whole copilot config — Steering Prompt, mission, and
steers. In the `/autopilot` menu, `e` exports the current config and `i` imports
one, stored under `~/.san/autopilot/<name>.json`.

## Relationship to other features

- [Permission model](../concepts/permission-model.md) — the static rules whose
  gray zone the Permission steer judges; hard-blocked actions never reach it.
- The judge component lives in `internal/reviewer` (`reviewer.Judge`); the
  steers and panel live in `internal/app` / `internal/app/input`.
