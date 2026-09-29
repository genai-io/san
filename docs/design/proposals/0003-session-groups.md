# PROP-0003: Session groups — sessions on one machine working together

## Status

Draft — 2026-09-29. An early prototype lives on the unmerged `feat/group`
branch and was verified end to end in tmux: two sessions messaging each other,
automatic naming, completion, and auto-detach when a group is deleted. This
page is the design after review; how it differs from the prototype is at the
end, under "Prototype vs. this design".

## Motivation

People often run several San sessions at once: one on the backend, one on the
frontend, one running a migration. Each works alone. When one learns something
another needs — an API changed, the migration finished — a person copies it
between terminals. Session groups let those sessions join a group whose agents
message each other directly.

**Goals**
- Sessions on one machine that join the same group can message each other by name.
- Each session knows who else is in the group, what each one owns, and who is online.
- Membership follows the session: a process exiting, or a session being
  resumed, never means joining again.

**Not in this version**
- Other machines or cloud sessions (needs a relay such as Remote Control).
- One session in several groups at once.
- `-p` headless sessions in a group.

## Concepts

| Concept | Meaning |
|---|---|
| **Group** | A directory, `~/.san/groups/<group>/`, created by the first join |
| **Member** | One **session** (not process) in the group: a name (`@api`), a role, a mode, online or offline |
| **Inbox** | One directory per member; each message addressed to it is one file |
| **Mode** | Whether a member wakes for a message: `active` (default) or `passive` |
| **Roster** | The member list, kept by the San process and told to the agent in reminders |

**A session is in at most one group at a time.** So `detach` takes no argument,
and a message is addressed by name alone.

## Overview

The path of one message from A to B:

```
 Session A (@api)                                      Session B (@web)
     │
     │ agent calls SendMessage(to: "web", message: "...")
     ▼
 writes ~/.san/groups/dev/web.inbox/<timestamp>-api.json
                                                           │
                                   B polls its inbox every second, reads, deletes
                                                           ▼
                                 ┌──────────── B's mode ────────────┐
                                 │                                  │
                          active (default)                       passive
                  injected as a user message:             queued as a reminder, shown
                  idle    → starts a turn                 to the person; reaches the
                  running → between tool calls            agent with their next input
```

When B has something to say back, it writes into `api.inbox/` the same way.

### Member messages are San's fourth input source

| Source | What | Code |
|---|---|---|
| Source 1 | The person's keyboard input | `input.Model` |
| Source 2 | Agent notices: subagent completions, interim reports, self-learn | `mainNotices` / `notify.go` |
| Source 3 | System events: cron, hooks, file watcher | `trigger.Model` |
| **Source 4** | **Member messages and roster changes** | **`internal/app/member` (new)** |

Source 4 follows different rules from Source 2, so it is a source of its own
rather than a case inside Source 2:

| | Source 2 (subagents) | Source 4 (members) |
|---|---|---|
| Wakes the agent | always | per its own mode (active / passive) |
| Trust | a subagent this session spawned | another session, not the person; never approves anything |
| Count | none | `unattended-turns` |
| Other events | none | joins, leaves, online/offline, renames |
| Lifetime | the task's | the membership's; queued while offline, delivered on resume |

**Only the delivery timing is shared**: start a turn when idle, insert between
tool calls when running, hold while a stream owns the tail. That part moves
out of `notify.go` into one delivery seam that Source 2 and Source 4 both call.

The broker is not involved; it still routes only between the main conversation
and its subagents, in-process.

## Storage

```
~/.san/groups/<group>/<name>.json      member: id, name, role, mode, sessionID, pid, cwd, joinedAt
~/.san/groups/<group>/<name>.inbox/    <nanoseconds>-<sender>.json
```

- **Named after the member**, so a listing reads at a glance. Join creates the
  member file exclusively, so the filesystem guarantees no two members share a
  name — two sessions racing for one name cannot overwrite each other.
- **Member `id`**: made at join and never changed, so a rename is recognised as
  one. Not the session ID, which changes on `/clear`.
- **Message file names** put the timestamp first: name order is time order, so
  messages arrive in the order sent. A sender cannot send twice in one
  nanosecond, so no random suffix is needed.
- **Contents**: sender, body, time sent.
- **No shared writes.** A member writes only its own file; a sender only adds
  files to the recipient's inbox. Concurrent sessions never race.
- **Atomic writes.** Write a temp file (leading `.`, trailing `.tmp`), then rename; readers skip temp files.
- **Permissions.** Directories 0700, files 0600: only the current OS user.
- Directories rather than sockets: cross-platform (Windows included), no auth
  token, and messages queue while a member is offline.

## Membership belongs to the session

Membership belongs to the **session** (the conversation), not the process. A
process is only the session's current host: it exits, and the session comes
back.

| Event | Handling |
|---|---|
| **join** | Creates the group if missing (default `default`). The group, name, role, mode and member `id` are stored in the session record |
| **Automatic naming** | Without `--as` / `--role`, the model summarizes the conversation into a name and role; with no conversation or on failure, the `/name` session name or the directory name |
| **Process exits** (`/quit`, crash, terminal closed) | **Stays in the group, offline**: member file and inbox remain. A clean exit's finalizer sets `pid` to 0, so it shows offline at once; after a crash, the pid check finds it offline |
| **Message to an offline member** | Queued in its inbox as usual; the sender is told `@web is offline; it reads this when it resumes` |
| **Session resumed** (`san -r`, `/resume`) | Reads the group from the session record, reclaims its member file by `id`, writes the new `pid`, and is online — **no join needed**; queued messages arrive |
| **`/clear`** | Keeps membership (the role has not changed); the full roster is attached again |
| **`/resume` to another session inside San** | The old session goes offline; the new one comes online if it is in a group |
| **`/group detach`** | Actually leaves: member file and inbox removed, group dropped from the session record. The last member out removes the group directory |
| **`/group remove <name>`** | Removes the named member, usually one long offline |
| **`/group delete`** | Removes the group directory. Online members detach within a second and say so; an offline member finds out when it resumes |

**No heartbeat, and no automatic cleanup of offline members.** Online means the
process at the member file's `pid` is alive; nothing is written periodically,
and a sleeping laptop is not mistaken for a dead one. People remove members
they no longer want with `remove`.

### Finalizers

When a session creates state that outlives it — a group membership — it
registers a cleanup. Every exit path (`/quit`, Ctrl+D, Ctrl+C, a failed run)
ends where `tea.Run` returns, and finalizers run there. Here the finalizer
**marks the member offline** (`pid` 0); it does not leave the group.

A process killed before it can run them reads as offline through the pid check,
to the same effect. Group membership is the only such state today, so the
finalizers are a plain name → cleanup map, not a framework.

## Keeping the roster

The San process keeps it; **the model maintains nothing** and only reads
reminders. None of this depends on active or passive.

```
disk (the source of truth)   ~/.san/groups/dev/*.json
        │  read every second
        ▼
process memory (snapshot)    map[member id] → {name, role, mode, online, cwd}
        │  diffed; only changes reach the model
        ▼
model context                full roster (join, resume, compaction) + change reminders
```

Every second, read every member file and compare its **contents** with the
snapshot (not file times):

| Change | Detected by | The model is told |
|---|---|---|
| Joined | on disk, not in the snapshot | `Group dev: @web joined (active) — owns the login page` |
| Left / removed | in the snapshot, not on disk | `Group dev: @web left` |
| Offline / online | its `pid`'s process is gone / back | `Group dev: @web went offline` / `is back online` |
| Updated | same member `id`, role or mode changed | `Group dev: @web is now passive` |
| Renamed | same member `id`, name changed | `Group dev: @web is now @frontend` |

The same pass also handles:
- Its own member file gone while the group remains (it was `remove`d) → leaves and says so.
- The group directory gone (`delete`) → leaves and says so.

SendMessage checks **the disk** when it sends, not a snapshot up to a second
old. A name that is not a member fails with the current members listed, so the
model can correct itself.

## Modes: the receiver decides whether it wakes

**Principle: whether a message makes an agent run is decided only by the
receiver's mode; a sender cannot change it.**

| | **Active (default)** | **Passive** |
|---|---|---|
| Message arrives, **idle** | Injected as a user message; **starts a turn** | Queued as a reminder, rides on the person's next input; **never starts a turn** |
| Message arrives, **running** | Inserted between tool calls | **Also only queued**; not inserted into the running turn, so a person-led task is not hijacked |
| The person sees | `◆ Message from @x: …` | `◆ Message from @x: … (queued for your next message)` |
| Fits | Sessions meant to collaborate on their own | Sessions a person is driving and doesn't want interrupted |

Active is the default because joining a group is already the person choosing
to collaborate. `/group mode passive` switches at any time; the mode is written
to the member file, so the group sees it.

**Senders know the recipient's state.** The roster shows each member's mode and
online status, and SendMessage's result says which applies:
- active and online: "Delivered; they will handle it now."
- passive: "Delivered; they see it when their person next interacts — don't wait for a reply."
- offline: "Queued; they see it when the session resumes."

## Injection: what, when, and in what form

| Event | Form | Target running | Target idle | The person sees |
|---|---|---|---|---|
| Peer message → active | user message | inserted between tool calls | starts a turn | `◆ Message from @x` |
| Peer message → passive | reminder | queued | queued | `◆ Message from @x … (queued for your next message)` |
| Roster change (join, leave, offline/online, update, rename) | reminder, delta only | inserted between tool calls | queued | one line, e.g. `@web joined group dev` |
| Group deleted / self removed | reminder | same | same | `Your group was deleted — left it.` |
| Full roster | reminder provider | on join, resume, `/clear`, after compaction | same | — |

**How**: the shared delivery seam (extracted from `notify.go`) takes two kinds;
Source 4 picks one per event and mode.
- **Reminder kind**: inserted into the running turn when there is one (except
  a passive member's peer message, which only queues); queued when idle; never
  starts a turn.
- **Message kind**: unchanged.

## Preventing endless exchanges: the agent judges

There is no hard turn cap. Instead the agent gets what it needs to judge.

**`unattended-turns`**: how many turns in a row group messages have started
since the person last typed, this one included, counting from 1.

| Situation | Count |
|---|---|
| A peer message starts a turn | +1 |
| Several peer messages delivered as one turn | +1 (per turn, not per message) |
| A peer message inserted into a running turn | unchanged; the message carries the current value |
| Queued for a passive member | unchanged |
| The person types in this session | reset to 0 |
| Session restarts | starts at 0 (memory only) |

**The receiver counts it.** It measures how long *this* session's person has
been away, which only the receiver knows. The sender's message file carries no
count, so it cannot be forged.

**The model is told what it means** once, in the roster reminder (attached on
join and after compaction):

```
Messages from members arrive as <group-message from=".." unattended-turns="N">.
unattended-turns is counted by this session: how many turns in a row group
messages have started since your user last typed, this one included. It
resets to 0 when your user types.

A rising count means agents are running on their own. Before replying,
check that the exchange is converging on a result. If you are repeating
yourself, answering only to acknowledge, or waiting on each other, stop:
don't reply, and leave your user a one-line note of where things stand.

When a member asked you for something, tell them when it is done — or that
you can't do it. That reply moves the work forward; a bare "got it" does not.
```

The last paragraph keeps "don't answer only to acknowledge" from being read as
"stay silent when done": a request gets an answer when it is done or cannot be.
That answer is the completion signal, so there is no separate read receipt.

and each peer message ends with the current value:

```
<group-message from="web" unattended-turns="7">
schema changed, please rebase
</group-message>
(Unattended turn 7: group messages have started 7 turns in a row since your user last typed.)
```

**The person sees it too**: from the second turn on, the notice carries it,
e.g. `◆ Message from @web: schema changed · 7th since you last typed`.

The rest of the design pulls the same way:
- Roster changes are reminders: they start no turn and offer nothing to answer.
- Passive members are never woken by peers.

## SendMessage: for group members first

- While in a group, the main agent has SendMessage turned on; it goes back off
  on leaving. (It is off by default for the main agent because steering a
  running background subagent was never used in practice.)
- Its description leads with group members: `to` is a member's name.
- The task-id path to subagents, and a subagent's `"main"` report, stay but
  are no longer advertised; they can go once confirmed unused.
- The body is wrapped in `<group-message>`; the call shows as `Message → @web: …`.

## Safety

- A peer message is marked as coming from another session, not the person: it
  never approves a permission, never justifies changing settings or
  AGENTS.md, and a slash command in it is plain text.
- The receiver's own permission checks still apply.

## Caching and cost

- Roster, change notices and peer messages all ride the reminder or
  message channel; **the system prompt never changes**, so the prompt-cache prefix holds.
- join / detach rebuild the agent once (SendMessage turns on or off), costing one cache miss. Both are rare.
- Passive, offline and idle members spend nothing when others message or come and go.

## Commands

| Command | Does |
|---|---|
| `/group join [group] [--as NAME] [--role TEXT] [--passive]` | Join, creating the group if missing; name and role summarized when omitted; active by default |
| `/group mode active\|passive` | Switch this member's mode now; the group sees it |
| `/group list` | In a group: members with name, mode, online status, role, directory. Otherwise: every group and its size |
| `/group detach` | Leave the current group |
| `/group remove <name>` | Remove a member, usually one long offline |
| `/group delete [group]` | Remove a group |

**Completion**: `/gro` → subcommands → after `join`, the existing groups and
their members (or `default` to create); after `mode`, `active` / `passive`;
after `remove`, the members, offline first.

## Where it lives

| File | Responsibility |
|---|---|
| `internal/group/group.go` | Member files, inboxes, pid check; Join / Detach / Remove / Delete / Send / Poll |
| `internal/app/member` (new, Source 4) | Inbox poll, roster snapshot and diff, wake or queue per mode, `<group-message>` wrapping, `unattended-turns` |
| `internal/app/group.go` | `/group`, automatic naming, finalizers, completion |
| Session record (`internal/session`) | Stores the group, so a resume comes back online |
| `internal/tool/agent/sendmessage.go` | Resolves member names; the result reflects the recipient's state |
| Shared delivery seam (extracted from `internal/app/notify.go`) | Delivery timing — wake, insert, hold, queue — for Source 2 and Source 4 |
| `internal/app/kit/suggest`, `input/on_textarea.go` | Argument completion; opening the next level after a selection |

## Prototype vs. this design

The `feat/group` prototype built an earlier iteration. What changes to land
this design:

| Prototype | This design |
|---|---|
| Files named by random id; messages `<timestamp>-<random>` | Named by member; messages `<timestamp>-<sender>`; exclusive create on join |
| 10s heartbeat, reaped after 60s stale | No heartbeat; online/offline from `pid`; no automatic cleanup |
| Process exit leaves the group | Process exit goes offline; membership stored with the session and restored on resume |
| Active behaviour only, at most 5 turns without the person | Active by default, passive available; no cap, `unattended-turns` instead |
| Full roster on change, riding the next message | Deltas only; inserted when running, queued when idle; shown to the person; renames and online status recognised |
| No `mode` or `remove` | Both |

Two existing bugs the prototype fixed, kept when landing:
- **A rebuild stopped the new agent.** The replaced agent's stop event arrived
  late and stopped the agent that replaced it, so the next send failed with
  `agent session is not active`. Toggling a tool in `/tool` could hit it too.
- **SendMessage rendered as a subagent spawn.** It now reads `Message → @x: …`.

## Later

- **Coordinator mode**: wake a chosen member when someone joins, e.g. to hand the newcomer work.
- A group badge in the status bar, e.g. `group:dev(3)`.
- `/group rename` (renames are already recognised by member `id`).
- Messaging across machines.

## Open questions

1. **Passive by default under YOLO?** Active plus YOLO means a peer's request
   runs with no one confirming it; a session that read a malicious page could
   use a group message to steer a YOLO peer. Leaning: passive by default under
   YOLO, switchable to active.
2. **Should subagents message group members directly?** Today they can, and
   the sender shows as this session's name.
