# Autopilot

## Overview

Autopilot is San's autonomy system, designed to minimize human intervention: a
copilot model cruises the session, keeping routine work moving and handing
control back only when something genuinely needs you. You give it a mission, a
system prompt and a model, and you let it do four things on your behalf:
suggest your next input, approve permission requests, answer questions, and
continue toward the mission turn after turn. Suggest and Approve are on by
default.

Configure it with the `/autopilot` panel, then engage it with `shift+tab` (cycle
until the amber `⏵⏵ autopilot`). A resumed session (`san -r <id>`) comes back in
the mode it was saved in. If you just want to see it drive, [`/goal`](#goal) is
the shortest path in.

## The panel

`/autopilot` is one page in three groups:

```
✦ Autopilot
GIVE IT
▸ Mission    the goal it works toward                      not set · Space to write
  System     how it thinks and decides (system prompt)     built-in
  Model      which model makes the calls                   same as session

LET IT
  [✓] Suggest   your next input · Tab to accept
  [✓] Approve   permission requests · asks you if risky
  [ ] Answer    questions and [y/N] prompts
  [ ] Continue  to the next turn · needs a mission

PRESETS
  Save preset…   reuse this setup in other sessions
  Load preset…    3 saved

↑↓ navigate · Space edit/toggle · ←→ adjust · Enter save · Esc discard
```

`space` works the highlighted row (opens an editor or flips a switch), `←`/`→`
step the Continue limit, `enter` saves and `esc` discards. Saving applies the
edits to the live session and writes them to `settings.json` as the default for
new sessions; it never starts a run — see [Running a mission](#running-a-mission).

### Give it

- **Mission** — the goal it works toward. Every decision leans on it: the next
  step it suggests or sends, and the intent Approve and Answer weigh. The editor
  takes the whole mission as text (`alt+enter` for a newline, paste works);
  `ctrl+r` asks the copilot to refine the draft in place, `ctrl+c` clears it,
  `enter` or `esc` goes back. The row shows where the mission stands
  (`ready`, `running`, `paused`, `✓ done`).
- **System** — how it thinks and decides: the editable part of the copilot's
  system prompt, seeded with the built-in instructions. The safety rules are a
  separate, fixed policy every decision always receives; this edits only how it
  drives. Per session.
- **Model** — which model makes the calls. Optional: "same as session" follows
  the session model. Otherwise pick a connected provider, then one of its cached
  models, then — for a model that reasons — its thinking rung (`default` keeps
  the model's own). The pick is stored as `vendor/model`, so the copilot keeps
  that provider when you switch the session model. A cheap, fast model is
  usually enough; an explicit rung lifts the 512-token verdict cap so reasoning
  cannot starve the answer.

### Let it

None act unless Autopilot mode is engaged, except Suggest, which controls input
hints in every mode.

| Switch | Default | What it does |
|---|---|---|
| **Suggest** your next input | **on** | Shows a next-input suggestion as ghost text; `tab` accepts it, `enter` sends it. It never submits on its own. With a mission it proposes the next step toward it; otherwise it predicts what you would type. |
| **Approve** permission requests | **on** | Judges tool calls the static rules left to you, weighing reversibility, blast radius and data exfiltration. Under git, history is the safety net: changes to tracked files are routine, and git's own sharp edges (`reset --hard`, `clean -f`, force-push, …) are weighed against the mission rather than blocked outright. It still asks you about anything that leaves the tree, and fails closed: any error asks you. Loading a skill counts as reading instructions — any script it points to is judged when it runs. |
| **Answer** questions and [y/N] prompts | off | Answers the agent's `AskUserQuestion` when the mission or conversation makes a reasonable choice clear, and replies to an approved command's interactive prompt (`Continue? [Y/n]`) when the reply only continues it. Defers anything that is genuinely your call, and skips prompts that would widen scope. |
| **Continue** to the next turn | off | After each turn of a running mission, decides whether it is done and, if not, types the next step itself. Needs a mission. Bounded by its limit (`←`/`→`: 5, 10, 20, 50, 100 or no limit; default 20). |

## Running a mission

A mission has a state, and only a **running** mission is driven. That keeps the
mission's turns apart from your own: Continue never tacks the mission onto a
conversation you started.

| State | Meaning | Continue drives it | The composer shows |
|---|---|---|---|
| — | no mission | no | — |
| **ready** | written, not started | no | `Start the mission? enter to start · esc to skip` |
| **running** | the copilot is driving it | **yes** | — |
| **paused** | you stepped in, or the copilot handed back | no | `Resume the mission? enter to resume · esc to skip` |
| **done** | the copilot judged it accomplished | no | — |

- **Start.** Save a mission in the panel, then engage Autopilot with
  `shift+tab`: the composer offers to start it and `enter` does. The copilot
  derives the opening step and sends it. Already in Autopilot? The offer shows
  as soon as you save.
- **Your own messages.** Typing takes the composer — the offer steps aside and
  `enter` sends what you typed. That turn is yours: a running mission pauses,
  and when your turn ends the composer offers to resume it. Nothing you type is
  treated as a mission step.
- **Handing back.** When the copilot needs a decision only you can make, the run
  is cut short (`esc`, a stop hook, leaving Autopilot) or the limit is reached,
  the mission pauses. Resuming carries on the same count, with a fresh limit if
  it ran out.
- **Done.** When the copilot judges the mission accomplished it marks it done:
  it stays on record but is never driven again, and stops weighing on decisions.
  Your switches are left as you set them. To run another, write a new mission.
- **Changing the mission** — editing it, or loading a preset that carries one —
  makes it ready again, a fresh run.
- **Resuming a session** brings a running mission back paused, so nothing drives
  until you say so.

## /goal

The common case — state a mission, switch on what lets the copilot act, take the
limit off, start — collapses into one line:

```
/goal add table-driven tests for internal/setting until go test ./... passes
```

- the goal becomes the [mission](#running-a-mission), already running
- Answer and Continue come on
- the limit is lifted — the run ends when the goal is met, not when a counter
  expires
- Autopilot engages and the copilot opens the first step itself

Stated mid-turn, it takes over when the current turn lands. `/goal` on its own
reports the current goal; `/goal clear` drops it.

It is deliberately session-scoped: unlike saving the panel, it does not rewrite
your saved defaults. **Approve** is left exactly as configured, since an
explicit off there is a safety choice a goal has no business overriding. When
the goal is met or cleared, the switches rewind to what `/goal` found.

Reach for the panel instead when you want a different mix: a run that suggests
but never submits, a bounded number of continuations, or a custom system prompt.

## Staying autonomous

With Continue on, a running mission is driven through the things that would
otherwise park the session until you came back:

- **A turn that stopped mid-work** — step limit, or output truncated beyond
  recovery — is picked back up, with the copilot told how it ended. Your own
  `esc` is different: a cancelled turn is you taking the helm, and pauses the
  mission. So does a stop hook.
- **A turn that failed outright** gets a growing backoff (5s, 10s, 15s) and then
  a resume decision, up to three consecutive attempts — reset by any turn that
  reaches its end. An error that needs you still lands as a handback.
- **A copilot call that misfired** retries up to three times, so a network blip
  or a non-JSON reply doesn't end the mission.
- **A compaction mid-decision** holds the verdict instead of dropping it.
- **Running out of turns**: set Continue's limit to no limit and the run ends
  when the mission is done, not when a counter expires. The copilot's own
  judgement is then the only stop, so pair it with a clear completion test in
  the mission.

Uncapped runs pair well with a fast, cheap model — see [Model](#give-it).

## Demo: a hands-free scaffold

A two-minute run that exercises the full loop — kick-off, permission approval,
continuation, and completion — without touching anything outside a scratch
directory.

**1. Start San in an empty repository.** Run it there and nowhere else — the
goal below writes `notes/` wherever it starts, so in one of your own projects it
would scribble into a real directory:

```bash
mkdir /tmp/autopilot-demo && cd /tmp/autopilot-demo && git init -q && san
```

`git init` is not incidental — under git, Approve treats changes to tracked
files as recoverable, which is what keeps the run from stopping to ask.

**2. State the goal.** One line, and it is the last key you press:

```
/goal Scaffold a notes/ directory: todo.md with a 3-item checklist, done.md
empty, and README.md explaining the layout. Work one file per turn. When all
three exist, verify with ls notes/ — then the goal is met.
```

Three details in that wording are doing work: *one file per turn* forces several
continuations so you can watch them, *ls notes/* puts a judged bash call in the
path, and *then the goal is met* gives the copilot a completion test it can
actually check.

**3. Watch the run.** Expect a transcript like:

```
⏵ autopilot · goal set

❭ Create notes/todo.md with a 3-item checklist.
  ⎿  autopilot · step 1
● Write(notes/todo.md)
  ⎿  Write → 5 lines

❭ Create an empty notes/done.md.
  ⎿  autopilot · step 2
...
● Bash(ls notes/)
  ↳ auto-approved · read-only directory listing
  ⎿  Bash → 3 lines

✓ autopilot · mission complete
```

Every `❭` carries the green `⎿ autopilot` mark — the copilot typed them all,
opening step included; you never touched the composer. The `ls` is a call
Approve let through inline. On `✓ mission complete` the mission reads done and
the switches rewind to what `/goal` found — open `/autopilot` to confirm — while
Autopilot stays engaged. To stop early, `/goal clear`.

The same run through the panel: write the text above as the **Mission**, turn
**Continue** on, press `enter` to save, then `shift+tab` into Autopilot and
`enter` on the start offer. To see the gentle end of the spectrum, leave only
**Suggest** on: the copilot proposes each step as ghost text and you accept with
`tab` + `enter`.

## Reading the transcript

| Mark | Meaning |
|---|---|
| green `⎿ autopilot · 2/5` | the `❭` line above was typed by the copilot (continuation 2 of 5; an uncapped run counts `step 2` instead) |
| amber `⏵ autopilot · turn failed · retrying in 5s` | a turn errored out; the copilot will decide whether to resume |
| green `↳ auto-approved · <reason>` | Approve let the tool call above through |
| amber `↳ escalated · <reason>` | Approve sent the call back to you |
| green `⏵ autopilot · answered for you` | the copilot answered an `AskUserQuestion` |
| amber `↩ autopilot · this question is yours` | it deferred the question to you |
| amber `↩ autopilot · over to you` | it stopped and handed control back; the mission is paused (a decide error rides after it) |
| green `✓ autopilot · mission complete` | the mission is done |

While a decision is in flight the mode line reads `⏵⏵ autopilot · thinking…`;
approvals tally there too (`· 3 approved · 1 escalated`).

## Configuration

The model, the switches and the Continue limit are saved to `settings.json` as
the default for new sessions. The mission, its state and the system prompt are
per-session: they ride the transcript and restore on `/resume`, but are never
written as the default. To carry a mission or a custom system prompt to another
session, save a preset and load it there.

The system prompt controls how the copilot drives; it does not replace the
immutable control-plane policy. Every copilot decision always receives that
policy, which fixes the trust boundaries, fail-closed behavior, task-specific
safety rules, and output contract. `systemPrompt` / `systemPromptFile` supply
only the editable part.

```jsonc
{
  "autoPilot": {
    "model": "anthropic/claude-haiku-4-5", // empty = the session model
    "thinkingEffort": "low",               // empty = the model's default rung
    "systemPrompt": "…",                   // per-session; the panel never writes it here
    "systemPromptFile": "~/prompts/pilot.md", // persistent default; used when systemPrompt is empty
    "maxContinuations": 20,                // Continue's limit; -1 = no limit
    "steers": {
      "suggest": true,     // Suggest
      "permission": true,  // Approve; omit for the default (on)
      "question": true,    // Answer — the agent's questions
      "bashPrompt": true,  // Answer — a command's [y/N] prompts
      "turnEnd": true      // Continue
    }
  }
}
```

The panel's Answer switch sets `question` and `bashPrompt` together. The keys
keep their original names for compatibility; a leftover `"skill"` key is
ignored — skill loads go through Approve like any other call.

Presets bundle the whole setup — mission text, system prompt, model and
switches — under `~/.san/autopilot/<name>.json`. A preset is a template: it
never records where a run stood, so a loaded mission always starts ready.

## Relationship to other features

- [Permission model](../concepts/permission-model.md) — the static rules whose
  gray zone Approve judges; hard-blocked actions never reach it.
- The judge component lives in `internal/reviewer` (`reviewer.Judge`); the
  mission lifecycle and switches live in `internal/app`, the panel in
  `internal/app/input`.
