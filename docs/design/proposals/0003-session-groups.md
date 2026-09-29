# PROP-0003: Session groups — sessions on one machine working together

## Status

Draft — 2026-09-29. A prototype of the core lives on the unmerged `feat/group`
branch and was verified end to end in tmux: two sessions messaging each other,
automatic naming, completion, finalizers, and auto-detach when a group is
deleted. Items marked ⬜ were settled in review and are not built yet.

## Motivation

People often run several San sessions at once: one on the backend, one on the
frontend, one running a migration. Each works alone. When one learns something
another needs — an API changed, the migration finished — a person copies it
between terminals. Session groups let those sessions join a group whose agents
message each other directly.

**Goals**
- Sessions on one machine that join the same group can message each other by name.
- Each session knows who else is in the group and what each one owns.
- Quiet by default: a member's message never makes your agent run on its own,
  and agents cannot fall into an endless exchange.

**Not in this version**
- Other machines or cloud sessions (needs a relay such as Remote Control).
- One session in several groups at once.
- `-p` headless sessions in a group.

## Concepts

| Concept | Meaning |
|---|---|
| **Group** | A directory, `~/.san/groups/<group>/`. Created by the first join, gone when the last member leaves |
| **Member** | One session in the group: a name (`@api`), a role, a working directory, and a **mode** |
| **Inbox** | One directory per member; each message addressed to it is one file |
| **Mode** | How a member handles what it receives: `passive` (default) or `active` |
| **Roster** | The member list, told to the agent as a reminder |

**A session is in at most one group at a time.** So `detach` takes no argument,
and a message is addressed by name alone.

## Overview

The path of one message from A to B:

```
 Session A (@api)                                      Session B (@web)
     │
     │ agent calls SendMessage(to: "web", message: "...")
     ▼
 writes ~/.san/groups/dev/b2.inbox/<timestamp>-<random>.json
                                                           │
                                   B polls its inbox every second, reads, deletes
                                                           ▼
                                 ┌──────────── B's mode ────────────┐
                                 │                                  │
                          passive (default)                       active
                  queued as a reminder, shown to          injected as a user message:
                  the person; reaches the agent           idle    → starts a turn
                  with the person's next input            running → between tool calls
```

Each session's poll also:
- **Heartbeat**: rewrites its own member file every 10 seconds to show it is alive.
- **Roster changes**: when someone joins or leaves, tells its agent in a reminder and shows a line.
- **Group deleted**: when the group directory is gone, detaches.

- The broker is not involved; it still routes only between the main conversation and its subagents, in-process.
- Delivery reuses the existing injection seam, `notifyMain` — the path a subagent's report takes.

## Storage and concurrency ✅

```
~/.san/groups/<group>/<id>.json      member: name, role, cwd, mode, maxTurns, joinedAt
~/.san/groups/<group>/<id>.inbox/    messages: <nanoseconds>-<random>.json, name order = time order
```

- **No shared writes.** A member writes only its own file; a sender only adds
  files to the recipient's inbox. Concurrent sessions never race.
- **Atomic writes.** Write a temp file (leading `.`, trailing `.tmp`), then
  rename. Readers skip temp files, so a half-written message is never read.
- **Permissions.** Directories 0700, files 0600: only the current OS user.
- Directories rather than sockets: cross-platform (Windows included), no auth
  token, and messages survive a process restart.

## Lifecycle

| Event | Handling | |
|---|---|---|
| **join** | Creates the group if missing (default name `default`); a taken name gets `-2` | ✅ |
| **Automatic naming** | Without `--as` / `--role`, the model summarizes the conversation into a name and role; with no conversation or on failure, the `/name` session name or the directory name | ✅ |
| **detach** | Removes its member file and inbox; the last member out removes the group directory | ✅ |
| **delete** | Removes the group directory; members notice within a second, detach, and say so | ✅ |
| **Normal exit** | Runs finalizers | ✅ |
| **Crash / kill** | Heartbeat: the member file is rewritten every 10s; one untouched for 60s is removed by whoever reads the group | ✅ |
| **Wake from sleep** | A member removed while asleep rewrites its file at the next heartbeat and is back | ✅ |

### Finalizers

When a session creates state that outlives it — such as a group membership —
it registers a cleanup. Every exit path (`/quit`, Ctrl+D, Ctrl+C, a failed run)
ends where `tea.Run` returns, and finalizers run there.

A process killed before it can run them is cleaned up by the members that find
it gone — as a Kubernetes controller finishes a deleted object. Group
membership is the only such state today, so the finalizers are a plain
name → cleanup map, not a framework.

## Modes: the receiver decides whether it wakes

**Principle: whether a message makes an agent run is decided only by the
receiver's mode; a sender cannot change it.** Otherwise one active member could
wake every passive one and passive would mean nothing.

| | **Passive (default)** | **Active** |
|---|---|---|
| Message arrives, **idle** | Queued as a reminder, rides on the person's next input; **never starts a turn** | Injected as a user message; **starts a turn** |
| Message arrives, **running** | **Also only queued**; not inserted into the running turn, so a person-led task is not hijacked | Inserted between tool calls |
| The person sees | `◆ Message from @x: … (queued for your next message)` | `◆ Message from @x: …` |
| Fits | Sessions a person is driving | Unattended sessions that serve requests |

### Active turn cap ⬜

- Counts only **turns an active member's peers started consecutively while the
  person had not typed**.
- **Any input from the person resets it.**
- **Configurable**: default 20, `0` for no limit.
- At the cap the member behaves as passive (messages queue) and says so; the
  person's next input resets the count and restores active.

### Senders know the recipient's mode ⬜

- The roster shows each member's mode: `@web (passive): owns the login page`.
- SendMessage's result depends on the recipient's mode:
  - active: "Delivered; they will handle it now."
  - passive: "Delivered; they see it when their person next interacts — don't wait for a reply."

## Injection: what, when, and in what form

| Event | Form | Target running | Target idle | The person sees | |
|---|---|---|---|---|---|
| Peer message → **active** member | user message | inserted between tool calls | starts a turn (within the cap) | `◆ Message from @x` | ✅ (configurable cap ⬜) |
| Peer message → **passive** member | reminder | queued | queued | `◆ Message from @x … (queued for your next message)` | ⬜ |
| Someone joins / leaves | reminder, **delta only** | inserted between tool calls | queued | `@web joined group dev — role` | ⬜ |
| Group deleted | reminder | same | same | `Your group was deleted — left it.` | ✅ |
| Full roster | reminder provider | on join and after compaction | same | — | ✅ |

**How**: a notice carries a "reminder" kind, handled where `notify.go` decides
delivery timing.
- **Reminder kind**: inserted into the running turn when there is one (except a
  passive member's peer message, which only queues); queued when idle; never
  starts a turn.
- **Message kind**: unchanged — inserted when running, starts a turn when idle.

Roster changes are reminders **in both modes** and never wake anyone; an active
agent sees them the next time it runs.

## SendMessage: for group members first

- While in a group, the main agent has SendMessage turned on; it goes back off
  on leaving. (It is off by default for the main agent because steering a
  running background subagent was never used in practice.)
- ⬜ Its description leads with group members: `to` is a member's name.
- The task-id path to subagents, and a subagent's `"main"` report, stay but are
  no longer advertised; they can go once confirmed unused.
- ⬜ A name that is not a member fails with the list of current members, so the
  model can correct itself.
- The body is wrapped in `<group-message group=".." from="..">`; the call shows
  as `Message → @web: …`.

## Preventing endless exchanges

| Pair | Outcome |
|---|---|
| passive ↔ passive | never starts a turn on its own |
| active → passive | queued only; the passive side does not reply by itself, so no loop |
| active ↔ active | can loop → the consecutive-turn cap bounds it |

Two softer layers:
- Roster changes are reminders: they start no turn and offer nothing to answer.
- The roster tells the model to reply only when it moves the work forward, not
  to acknowledge. In testing, A received `ping` and asked its person what to do
  next instead of answering beta.

## Safety ✅

- A peer message is marked as coming from another session, not the person: it
  never approves a permission, never justifies changing settings or
  AGENTS.md, and a slash command in it is plain text.
- The receiver's own permission checks still apply.
- Passive by default means a peer's request waits for the person's next input;
  active is the person choosing to let it run. There is therefore no separate
  hold rule for YOLO mode.

## Caching and cost

- Roster, change notices and peer messages all ride the reminder or message
  channel; **the system prompt never changes**, so the prompt-cache prefix holds.
- join / detach rebuild the agent once (SendMessage turns on or off), costing one
  cache miss. Both are rare.
- Passive and idle members spend nothing when others message or come and go.

## Commands

| Command | Does | |
|---|---|---|
| `/group join [group] [--as NAME] [--role TEXT] [--active] [--max-turns N]` | Join, creating the group if missing; name and role summarized when omitted; passive by default | ✅ (`--active`/`--max-turns` ⬜) |
| `/group mode active\|passive [--max-turns N]` | Switch this member's mode now; written to its member file so the group sees it | ⬜ |
| `/group detach` | Leave the current group | ✅ |
| `/group list` | In a group: members with name, mode, role, directory. Otherwise: every group and its size | ✅ (mode ⬜) |
| `/group delete [group]` | Remove a group; its members detach | ✅ |

**Completion**: `/gro` → select → the subcommands appear → select `join` → the
existing groups and their members appear; with none, `default` is offered;
select `mode` → `active` / `passive` (⬜).

## Where it lives

| File | Responsibility |
|---|---|
| `internal/group/group.go` | Member files, inboxes, heartbeat, reaping; Join / Detach / Delete / Send / Poll |
| `internal/app/group.go` | `/group`, automatic naming, roster, poll and delivery, turn count, finalizers, completion |
| `internal/tool/agent/sendmessage.go` | Resolves member names; writes into the recipient's inbox |
| `internal/app/notify.go` | ⬜ Delivery timing for reminder-kind notices |
| `internal/app/kit/suggest`, `input/on_textarea.go` | Argument completion; opening the next level after a selection |

## Existing bugs fixed alongside ✅

- **A rebuild stopped the new agent.** The replaced agent's stop event arrived
  late and stopped the agent that replaced it, so the next send failed with
  `agent session is not active`. Toggling a tool in `/tool` could hit it too.
- **SendMessage rendered as a subagent spawn.** It now reads `Message → @x: …`.

## Later

- **Coordinator mode**: wake a chosen member when someone joins, e.g. to hand
  the newcomer work. Opt-in, off by default.
- A group badge in the status bar, e.g. `group:dev(3)`.
- Changing name or role after joining (today: detach and join again).
- Messaging across machines.

## Open questions

1. Is 20 the right default for the active turn cap?
2. Are a 10s heartbeat and a 60s stale threshold right?
3. Should subagents message group members directly? Today they can, and the
   sender shows as this session's name.
