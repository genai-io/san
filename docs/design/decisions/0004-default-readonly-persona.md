# ADR-0004: Default Read-Only Persona

## Status

Proposed — 2026-06-17.

## Context

San ships without a default persona. When no persona is selected, the system
prompt is assembled from built-in defaults and all tools are available —
including write operations like file editing, git commits, and shell commands.

This means a fresh San session has no read-only guardrails. Common starting
workflows — "explain this code," "why is this test failing," "what does this
package do" — don't need write access, but the user gets it anyway.

The goal is to ship a readonly persona with two core benefits:

1. **Environment protection**: The persona physically cannot modify the user's
   working environment — no file writes, no git mutations, no package installs.
   This eliminates the risk of an accidental or hallucinated destructive
   operation corrupting the project state.

2. **Token savings**: The default system prompt carries substantial weight from
   parts that are irrelevant to read-only work — engineering methodology,
   commit conventions, git protocols, task management rules, and safety
   constraints for destructive operations. A read-only persona replaces all
   three prose parts with minimal, purpose-specific prompts, dropping every
   word that does not serve reading, analyzing, or explaining.

## Core Model

A **"readonly" persona** distributed as a standard persona folder. Unlike the
earlier draft that fell back to San's default `behavior.md`, this persona
**overrides all three prose parts** with minimal, read-only-specific content.
Nothing from San's default engineering prompt survives — the model only sees
what a read-only assistant needs.

```
.san/personas/readonly/
├── system/
│   ├── identity.md       ← minimal: "You are a read-only assistant"
│   ├── behavior.md       ← minimal: how to analyze, explain, debug
│   └── rules.md          ← minimal: read-only constraint + what to do when asked to write
└── settings.json         ← permissions.deny for write tools (enforcement layer)
```

The persona is a regular persona directory — no engine changes required. It is
published at [github.com/genai-io/readonly-persona](https://github.com/genai-io/readonly-persona).
Users install it by cloning into `~/.san/personas/readonly/`. The main repo may
carry a lightweight reference to demonstrate the pattern and link to the
standalone repo, but does not own or maintain the full persona.

Each part is intentionally short. The design principle is: **if a sentence
does not help the model read, analyze, or explain, cut it.**

### Prompt content

`system/identity.md`:

```markdown
You are a read-only assistant. You answer questions, analyze code, and debug
environments. You never modify files, code, config, or system state.
```

`system/behavior.md`:

```markdown
## Analysis

When analyzing code: read the relevant files first, trace the logic
systematically, identify root causes before suggesting fixes. When
explaining: be clear and concise. Use concrete references (file paths,
line numbers, function names).

## Debugging

When debugging: check logs, inspect state, trace execution paths. Run
read-only diagnostic commands (ls, cat, grep, git log, git status,
git diff). Isolate the problem before explaining it.

## Communication

- Answer the question asked — no digressions.
- Prefer direct answers over exposition.
- If you need more context, ask rather than guess.
```

`system/rules.md`:

```markdown
## Read-only constraint

You are in read-only mode. The following are blocked:

- Creating, modifying, or deleting files
- Shell commands that write to the filesystem
- Git operations that change repository state (commit, push, merge, rebase,
  tag, etc.)
- Installing packages, dependencies, or system software
- Modifying any configuration

## What you can do

- Read files and search codebases
- Answer questions about code, architecture, design, and conventions
- Analyze bugs, trace execution paths, explain behavior
- Run read-only shell commands: ls, cat, grep, find, git log, git status,
  git diff, git show, git blame, and similar
- Debug environments and diagnose issues

## If asked to write

Explain that you are in read-only mode and cannot perform write operations.
Suggest switching to a persona with write access via `/persona <name>`.
```

### Why override behavior.md

San's default `behavior.md` carries ~30 lines of engineering methodology:
work habits (read design docs, follow layered architecture, write unit tests,
run lint), communication style (report what you changed, link PRs), and scope
discipline. None of this applies to a read-only session. Overriding it with a
minimal alternative recovers those tokens and gives the model guidance
actually relevant to reading and analyzing.

### Why override rules.md

San's default `rules.md` bundles policy, tool protocols, task/git conventions,
safety rules for destructive commands, and provider-specific quirks. For a
read-only session these are dead weight — the persona cannot write anyway, so
rules about commit messages, PR conventions, and `--no-verify` are noise.
A minimal replacement keeps only the readonly constraint.

### Token savings estimate

Rough line counts for San's built-in defaults vs. the readonly replacements:

| Part | Default (approx. lines) | Readonly (approx. lines) | Savings |
|---|---|---|---|
| `identity` | ~3 | ~2 | small |
| `behavior` | ~30 | ~12 | ~60% |
| `rules` | ~120 | ~18 | ~85% |
| **Total** | **~153** | **~32** | **~80%** |

These savings apply to every turn where the system prompt is included
(including cache misses), and because the persona prompt is part of the
prompt-cache prefix, the smaller prompt also means a smaller cache entry.

### Read-only operations (allowed)

| Category | Examples |
|---|---|
| Read files | `Read`, `cat`, `head`, `tail` |
| Search | `grep`, `find`, `git grep` |
| Git read | `git log`, `git status`, `git diff`, `git show`, `git blame` |
| Analyze | code review, architecture analysis, bug tracing |
| Answer | questions about code, design, conventions |
| Debug | trace errors, inspect logs, check environment state |

### Write operations (blocked)

| Category | Examples |
|---|---|
| File write | `Edit`, `Write`, shell redirect (`>`, `>>`), `tee` |
| File delete | `rm`, `rmdir`, `shred` |
| File move/copy | `mv`, `cp`, `dd` |
| File create | `touch`, `mkdir` |
| Permissions | `chmod`, `chown` |
| Git write | `commit`, `push`, `merge`, `rebase`, `tag`, `am`, `cherry-pick`, `stash` |
| Package install | `go install`, `npm install`, `pip install`, `make install`, `brew install` |
| Destructive | `rm -rf`, force push, `git reset --hard` |

## Decision

### D1: Ship as a persona folder, not built into the binary

The readonly persona is a standard persona directory — the same shape as any
user-created persona. No engine changes, no `embed.FS`, no special loading
path. The existing persona system already supports everything it needs:
`system/{identity,behavior,rules}.md` plus a `settings.json` with
`permissions.deny`.

It is published at
[github.com/genai-io/readonly-persona](https://github.com/genai-io/readonly-persona).
Users install it by cloning into `~/.san/personas/readonly/` or
`.san/personas/readonly/`. Once installed, `/persona readonly` works and it
appears in the `/persona` selector.

The main San repo may carry a lightweight reference (a README pointer or a
minimal example) to demonstrate the pattern and link to the standalone repo,
but does not own or maintain the full persona.

### D2: Not the system default (initially)

The readonly persona is **available by default**, but the system default
behavior (when no persona is selected) remains unchanged for now — full access.

Users opt into readonly mode via `/persona readonly` or by configuring
`persona: readonly` in their settings. Making it the system default is
deferred to a future decision after migration impact is assessed.

**Rationale**: Changing the default persona breaks the expectation that
a fresh San session has full tool access. This is a migration that requires
communication, not just code. Ship the persona first; flip the default
later if the community agrees.

### D3: Override all three prose parts — no default fallback

The persona provides its own `identity.md`, `behavior.md`, and `rules.md`.
None of San's default prose parts are used. This is deliberate:

- **Token efficiency**: The default parts carry substantial content for
  engineering workflows. Dropping them saves ~80% of the system prompt prose
  (see estimate above).
- **Signal clarity**: The model receives only instructions relevant to its
  read-only task. No engineering methodology, git protocols, or commit
  conventions dilute the prompt.
- **No dead rules**: Rules about `--no-verify`, commit message format, branch
  naming, and PR descriptions are noise when the persona cannot write.

Each part is kept minimal — the design goal is the shortest prompt that
produces correct read-only behavior.

### D4: Enforcement — permissions.deny (primary) + rules.md (advisory)

Two-layer defense, consistent with the persona model's design philosophy:

1. **`settings.json` permissions.deny** — the *enforcement* layer. Blocks
   write tools at the permission engine before they reach the model. Cannot
   be loosened by lower layers (user/project settings), per the persona
   permission merge semantics.

2. **`system/rules.md`** — the *advisory* layer. Natural-language constraints
   the model reads each turn. Guides the model away from write operations
   even if a tool slips through the deny list.

```json
{
  "description": "Read-only persona — answers questions, analyzes code, debugs environments. Cannot write.",
  "skills": {},
  "agents": [],
  "disabledTools": {},
  "permissions": {
    "defaultMode": "default",
    "deny": [
      "Edit",
      "Write",
      "Bash(rm:*)",
      "Bash(rmdir:*)",
      "Bash(mv:*)",
      "Bash(cp:*)",
      "Bash(touch:*)",
      "Bash(mkdir:*)",
      "Bash(dd:*)",
      "Bash(shred:*)",
      "Bash(chmod:*)",
      "Bash(chown:*)",
      "Bash(git commit:*)",
      "Bash(git push:*)",
      "Bash(git merge:*)",
      "Bash(git tag:*)",
      "Bash(git rebase:*)",
      "Bash(git reset:*)",
      "Bash(git am:*)",
      "Bash(git cherry-pick:*)",
      "Bash(git stash:*)",
      "Bash(go install:*)",
      "Bash(npm install:*)",
      "Bash(yarn add:*)",
      "Bash(pip install:*)",
      "Bash(pip3 install:*)",
      "Bash(brew install:*)",
      "Bash(make install:*)",
      "Bash(curl * | *)",
      "Bash(wget -O:*)"
    ]
  }
}
```

### D5: Git hooks — evaluated and deferred

Git hooks were considered as a third enforcement layer but are deferred:

- **Pro**: A pre-commit hook could block commits at the Git level, independent
  of San's tool permissions. This is defense-in-depth.
- **Con**: Hooks only cover Git operations, not general file writes. The
  permission engine already covers the full tool surface. Hooks require
  explicit installation per repository. The active persona is a San runtime
  concept — hooks have no native way to query it.

**If needed later**: Ship a `san persona current` CLI command that prints the
active persona. A pre-commit hook can call it:

```bash
#!/bin/bash
if [ "$(san persona current 2>/dev/null)" = "readonly" ]; then
  echo "Commits are blocked in readonly persona mode."
  exit 1
fi
```

This is a future extension; not part of this ADR.

### D6: Subagent inheritance

When the readonly persona spawns subagents (via the Agent tool), the readonly
constraint propagates through the permission layer — subagents use the same
effective settings overlay. No special subagent handling is needed.

## Implementation

### Phase 1 — Persona folder

Create the persona directory:

```
.san/personas/readonly/
├── system/
│   ├── identity.md
│   ├── behavior.md
│   └── rules.md
└── settings.json
```

Write the three prompt files (`identity.md`, `behavior.md`, `rules.md`) as
defined in [Prompt content](#prompt-content) above, and `settings.json` with
the deny list from [D4](#d4-enforcement--permissionsdeny-primary--rulesmd-advisory).

### Phase 2 — Publish as standalone repo

Published at [github.com/genai-io/readonly-persona](https://github.com/genai-io/readonly-persona).
Users install via:

```bash
git clone https://github.com/genai-io/readonly-persona.git ~/.san/personas/readonly
```

### Phase 3 — In-repo reference (optional)

Optionally add a pointer or minimal example in the main San repo to
demonstrate the persona pattern and link to the standalone repo — without
committing core to maintaining the full persona.

## Consequences

### Positive

- **Environment protection**: The working directory is safe from accidental
  modification. No file corruption, no unintended git mutations, no
  hallucinated destructive commands.
- **Token savings (~80%)**: The persona replaces ~153 lines of default
  system prompt prose with ~32 lines of read-only-specific content. The
  smaller prompt saves tokens on every turn and reduces prompt-cache size.
- **Signal clarity**: The model only receives instructions relevant to
  reading, analyzing, and explaining — no engineering methodology or git
  protocols dilute the prompt.
- **Safe starting point**: Users can explore, ask questions, and analyze code
  without risk of accidental writes.
- **Intentional escalation**: Write operations require a conscious persona
  switch — making the user's intent explicit.
- **No new infrastructure**: Reuses the existing persona system, permission
  engine, and settings overlay — no new enforcement mechanisms.

### Negative / costs

- **Deny list coverage**: The deny list must explicitly enumerate every write
  tool and command pattern. A new write-capable tool added to San (or a
  creative shell command not matching the deny patterns) could slip through.
  The advisory `rules.md` mitigates but does not eliminate this gap.
- **Not a sandbox**: San personas operate at the user's trust level — this is
  a guardrail, not a security boundary. A determined user or a plugin can
  bypass it.
- **User confusion**: Users who expect to edit files out of the box will be
  blocked when they switch to readonly mode. The persona selector must
  clearly communicate what each persona allows.

## References

- [`persona.md`](../../concepts/persona.md) — persona system design
- [`permission-model.md`](../../concepts/permission-model.md) — permission engine
- [`ADR-0001`](0001-layered-package-architecture.md) — layered package architecture
- [`ADR-0002`](0002-autonomous-dev-management.md) — autonomous dev team (persona config examples)
