# Autopilot

A copilot model keeps routine work moving and hands control back only when
something needs you. Configure it with `/autopilot`, engage it with `shift+tab`
(the amber `⏵⏵ autopilot`), or start a run in one line with [`/goal`](#goal).

## The panel

```
ALWAYS
▸ [✓] Suggest   your next input · Tab to accept
  [✓] Recap     when you're back · after 5m away

WHILE IT DRIVES · shift+tab to drive
      Mission   the goal it works toward          not set · Enter to write
      System    how it thinks and decides (system prompt)       built-in
      Model     which model makes the calls              same as session
  [✓] Approve   permission requests · asks you if risky
  [ ] Answer    questions and [y/N] prompts
  [ ] Continue  to the next turn · needs a mission

      Save preset…  reuse this setup in other sessions
      Load preset…  3 saved
```

`enter` or `space` edits or toggles a row, `←`/`→` step Recap's wait and
Continue's limit, `esc` saves and closes. Saving never starts a run.

Always works in every mode:

- **Suggest** — ghost-text next input after each turn. While Autopilot drives a
  mission, it proposes the next step toward it.
- **Recap** — when the terminal regains focus after the wait, one note on where
  the session stands: San's own facts (what waits on you, plan progress,
  mission, background work) and the goal, now, and next step. `/recap`
  shows one any time. If a turn is still running when you return, that
  automatic recap is skipped (not shown after the turn ends). Needs a terminal
  that reports focus (in tmux: `set -g focus-events on`). While generating,
  only `※` gently breathes, with no `writing…` or Recap label. When ready,
  the same `※` stays static beside the dim, italic summary; optional facts
  remain below it.

Both are side requests, like `/btw`: they reuse the session's prompt, tools and
conversation, so the provider's prompt cache serves them, and the agent never
sees them. Recap is written while you are away, before the cache expires.

While it drives — these act only in Autopilot mode, and are drawn dim outside it:

- **Mission** — the goal every decision leans on.
- **System** — the editable part of the copilot's system prompt. The safety
  rules are fixed and always apply.
- **Model** — optional; pick provider, model, then thinking rung. Stored as
  `vendor/model`, so it keeps its provider when the session model changes.
- **Approve** — judges permission requests; risky or failed checks go to you.
  A skill load is safe; scripts it runs are judged as Bash calls.
- **Answer** — answers `AskUserQuestion` and a command's `[y/N]` prompts.
- **Continue** — after each turn of a running mission, sends the next step until
  it is done or the limit (default 20) is reached.

## Running a mission

Only a **running** mission is driven, so your own messages are never continued
toward it.

| State | Meaning | Composer |
|---|---|---|
| ready | written, not started | `Start the mission? enter` |
| running | the copilot drives it | — |
| paused | you stepped in, or it handed back | `Resume the mission? enter` |
| done | accomplished; never driven again | — |

Sending your own message pauses a running mission. Editing the mission or
loading a preset makes it ready again. A resumed session brings it back paused.

## /goal

```
/goal add table-driven tests for internal/setting until go test ./... passes
```

Sets the mission running, turns Answer and Continue on with no limit, and starts.
`/goal clear` drops it. Either way the switches rewind to what `/goal` found.

## Configuration

```jsonc
{
  "autoPilot": {
    "model": "anthropic/claude-haiku-4-5", // empty = session model
    "thinkingEffort": "low",               // empty = model default
    "systemPromptFile": "~/prompts/pilot.md",
    "maxContinuations": 20,                // -1 = no limit
    "recapAfterMinutes": 5,                // -1 = only on /recap
    "steers": {
      "suggest": true,     // Suggest
      "permission": true,  // Approve
      "question": true,    // Answer
      "bashPrompt": true,  // Answer
      "turnEnd": true      // Continue
    }
  }
}
```

The mission, its state and an inline system prompt are per session and never
written here; presets (`~/.san/autopilot/<name>.json`) carry them across
sessions, without the state.
