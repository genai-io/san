# System Prompt

How Gen Code builds, mutates, and renders the system prompt for the main
agent and subagents.

## Mental model

The system prompt is **not** a single template. It is a layered structure
of named **Sections**, each owning a **Slot** (render order). Sections are
cached individually; mutating one re-renders only that section.

This buys three things:

1. **Stable cache prefix.** Volatile content (date, hook reminders) lives at
   the end so the prompt cache survives daily rollovers and ad-hoc updates.
2. **Hot-patching.** Activating a skill or switching cwd does not require
   rebuilding the agent — `sys.Use/Drop/Refresh` mutates the live System.
3. **Role-aware defaults.** Subagents and the main agent share the same
   policy/guidelines but get different identity and capabilities.

```
core.System
├── identity        (slot 0) — who you are
├── provider        (slot 1) — provider quirks
├── policy          (slot 2) — safety contract
├── guidelines      (slot 3) — tool usage, git, tasks, questions
├── memory          (slot 4) — user + project memory
├── capabilities    (slot 5) — skills, agents directories
├── invocation      (slot 6) — active /skill body
├── environment     (slot 7) — cwd, git, date  (volatile)
└── notice          (slot 8) — hook reminders  (volatile)
```

## Slot reference

| Slot | Section | Source | Volatility |
|------|---------|--------|------------|
| 0 | `identity` | `prompts/identity.txt` (default), `WithIdentity(text)`, `WithSubagentIdentity(brief)` | session-stable |
| 1 | `provider` | `prompts/providers/<name>.txt` | session-stable |
| 2 | `policy` | `prompts/policy.txt` | always |
| 3 | `guidelines-tools` | `prompts/guidelines/tools.txt` | always |
| 3 | `guidelines-git` | `prompts/guidelines/git.txt` (only if git repo) | session-stable |
| 3 | `guidelines-tasks` | `prompts/guidelines/tasks.txt` (main only) | always |
| 3 | `guidelines-questions` | `prompts/guidelines/questions.txt` (main only) | always |
| 4 | `memory-user` | `~/.gen/GEN.md` / `~/.claude/CLAUDE.md` + rules | session-stable |
| 4 | `memory-project` | `.gen/GEN.md` / `CLAUDE.md` + rules + local | session-stable |
| 5 | `capabilities-skills` | `skill.Registry.GetSkillsSection()` (active skills only) | session-stable |
| 5 | `capabilities-agents` | `subagent.Registry.GetAgentsSection()` (main only) | session-stable |
| 6 | `invocation-skill` | active `/skill` body | per-turn |
| 7 | `environment` | cwd, git, platform, model, today's date | per-day, per-cwd |
| 8 | `notice-*` | hook-injected reminders | volatile |

**Order rationale:** stable content sits in low slots so the prompt-cache
prefix survives changes in slot 7+. Within a slot, sections render in
insertion order (not name order) so callers control fine-grained order.

## Build API

```go
// internal/core/system/builder.go
sys := system.Build(core.ScopeMain,
    system.WithProvider(client.Name()),
    system.WithIdentity(persona),       // optional override
    system.WithGitGuidelines(isGit),
    system.WithMemory(user, project),
    system.WithSkills(skillsBody),
    system.WithAgents(agentsBody),      // main only
    system.WithSkillInvocation(body),   // optional, per-turn
    system.WithEnvironment(env),
)
```

Stock sections (identity, policy, guidelines.tools, plus tasks/questions for
main scope) auto-apply when `Build` is called. Options register additional
sections or override stock ones by name.

### Scope-based defaults

`core.Scope` distinguishes who the prompt is for:

- **`ScopeMain`** — top-level interactive agent. All four guidelines, full
  capabilities, default identity.
- **`ScopeSubagent`** — spawned by Agent tool. Identity replaced via
  `WithSubagentIdentity(brief)`; `tasks` and `questions` guidelines omitted
  (main-only behaviors); `WithAgents` not called by default (subagents are
  leaves — no recursion).
- **`ScopeCompact`** — not built via `Build`; `system.CompactPrompt()`
  returns a one-shot string.

### Mutating at runtime

```go
sys.Use(SkillInvocationSection(body))     // user typed /skill
sys.Drop("invocation-skill")              // turn ended, deactivate
sys.Refresh("environment")                // cwd changed, re-render env
```

Per-section render output is cached; `Refresh(name)` invalidates one
section. The full prompt is also cached and rebuilt only when something
changes (`dirty` flag in `core.system_impl`).

## XML envelope

All non-identity sections wrap their body in a uniform tag, applied by
`system/catalog.go:wrap()`:

```xml
<policy>...</policy>
<guidelines name="tools">...</guidelines>
<memory scope="user">...</memory>
<skills>...</skills>
<agents>...</agents>
<invocation kind="skill">...</invocation>
<environment>...</environment>
<notice name="...">...</notice>
```

The identity section is rendered raw (no envelope) so it appears as the
familiar "You are X" preamble. For subagents, identity uses `<identity>`
attributes to surface mode info: `<identity mode="explore">...</identity>`.

Section bodies are returned as plain text by their providers
(`skill.Registry.GetSkillsSection()`, `subagent.Registry.GetAgentsSection()`,
etc.); the catalog adds the wrap.

## Progressive loading

Skills, agents, and identities all use the same disclosure pattern:

| Level | When | Content |
|-------|------|---------|
| 1 | Always (in slot 5) | Name + one-line description |
| 2 | On invocation / spawn | Full body loaded from `.md` file |
| 3 | On demand inside the body | Resource files (scripts, references, AGENT.md) |

This keeps the always-on prompt small. The full body of a skill or agent
only enters the LLM context when the user (or LLM) explicitly invokes it.

## Identity (custom personas)

Identity is the only slot that lets the user fully replace its default
content. Identities are markdown files:

```
~/.gen/identities/<name>.md           # user-level
.gen/identities/<name>.md             # project-level (overrides user)
```

Each file has frontmatter + body:

```markdown
---
name: ml-engineer
description: ML engineering specialist (PyTorch, JAX)
---

You are an ML engineer assistant ...

# Tone
...
```

The body lands directly in slot 0, replacing `prompts/identity.txt`.

**What belongs in an identity body:** persona / role definition, tone,
domain-specific behavior, code style preferences.

**What does NOT belong:** policy / security rules, git safety, tool usage
guidelines, task management. Those live in their own slots and always
apply, regardless of which identity is active.

### Activation flow

```
settings.identity ("ml-engineer")
        │
        ▼
identity.Registry.Active("ml-engineer")
        │  resolves user/project files; project wins
        ▼
Identity.Body  (markdown, no frontmatter)
        │
        ▼
BuildParams.IdentityText  →  system.WithIdentity(body)
        │
        ▼
core.System slot 0 replaced
```

Empty / missing / unknown name → `Active()` returns `""` → catalog uses the
built-in default. No errors; the user always gets a working prompt.

### `/identity` command

The `/identity` slash command unifies three actions:

| Form | Action |
|------|--------|
| `/identity` | Open read-only selector overlay |
| `/identity create [name-hint]` | Inject create workflow as PendingInstructions |
| `/identity edit <name>` | Inject edit workflow with target file |

The selector exposes `Shift+N` (create) and `Shift+E` (edit) hotkeys that
dispatch through the same handler. Workflow templates live in
`internal/command/builtin/identity-{create,edit}.md` (embedded), are loaded
by `command.BuiltinWorkflow(name)`, and instruct the agent to use
`AskUserQuestion` when intent is unclear, then write/edit the file using
its normal Read / Write / Edit tools.

There is no in-UI form or external editor invocation — file authoring is
the agent's responsibility, with the user supplying intent.

The user-level directory (`~/.gen/identities/`) and its `README.md` are
auto-created on startup (`identity.EnsureUserDir`) so the create workflow
has a format spec to read.

## Subagent identity replacement

For subagents, the identity slot is replaced entirely by a charter built
from the agent's config:

```go
// internal/subagent/executor_prompt.go
brief := system.SubagentBrief{
    AgentName:       config.Name,
    Description:     config.Description,
    Mode:            string(permMode),
    ToolConstraints: config.AllowTools.ConstrainedDisplayNames(),
    CustomPrompt:    config.GetSystemPrompt(),  // AGENT.md body
}
sys := system.Build(core.ScopeSubagent,
    system.WithSubagentIdentity(brief),
    ...
)
```

The brief renders as:

```xml
<identity mode="explore">
You are a code-reviewer subagent operating inside Gen Code.
Role: Reviews code changes for bugs, security, performance.

Operational scope: read-only research; do not modify files or run shell commands.
Tool constraints: Bash limited to git diff*, git log*, ...

{AGENT.md body}
</identity>
```

Subagents inherit policy, guidelines, and memory from the same templates as
the main agent — only identity, capabilities filtering, and main-only
guidelines (`tasks`, `questions`) differ.

## Skill / Agent injection

### Skills (Slot 5: `<skills>`)

`skill.Registry.GetSkillsSection()` returns a body listing **active** skills
only (state machine: Active / Enable / Disable, controlled by user via
`/skills`). Inactive skills are not in the prompt; full skill bodies are
loaded only when the LLM invokes the `Skill` tool or the user activates via
`/<skill-name>` slash command (which sets `Skill.PendingInstructions` for
the next turn).

### Agents (Slot 5: `<agents>`)

`subagent.Registry.GetAgentsSection()` returns a body listing all enabled
agent types with name + description + tool list. Only rendered for
`ScopeMain`; subagents do not see this directory.

### Invocation (Slot 6)

When a skill is active for the current turn, its full body is registered
as the `invocation-skill` section via `WithSkillInvocation(body)`. This
bypasses the registry's "summary only" output and gives the LLM the full
content for one turn.

## Memory injection

Memory comes from files (GEN.md / CLAUDE.md) loaded by `system.LoadMemoryFiles`:

```
~/.gen/GEN.md              ─┐
~/.claude/CLAUDE.md         ├── memory-user
~/.gen/rules/*.md          ─┘

.gen/GEN.md                ─┐
GEN.md (project root)       │
.claude/CLAUDE.md           ├── memory-project
CLAUDE.md (project root)    │
.gen/rules/*.md             │
.gen/GEN.local.md          ─┘
```

User and project memory are deduplicated (first source wins per level) and
joined with blank lines. They render as `<memory scope="user">` and
`<memory scope="project">` respectively.

## File map

| File | Role |
|------|------|
| `internal/core/section.go` | `Section`, `Slot`, `Scope`, `Source` types |
| `internal/core/system.go` | `System` interface (Use / Drop / Refresh / Sections / Prompt) |
| `internal/core/system_impl.go` | Default implementation; per-section + whole-prompt caching |
| `internal/core/system/builder.go` | `Build(scope, opts...)` entry point |
| `internal/core/system/catalog.go` | All section factories + `wrap()` envelope helper |
| `internal/core/system/memory.go` | `LoadMemoryFiles`, `LoadInstructions` |
| `internal/core/system/prompts/` | Embedded `.txt` templates (identity, policy, guidelines, compact) |
| `internal/identity/` | Identity registry, file parser, template generator |
| `internal/skill/registry.go` | `GetSkillsSection`, `GetSkillInvocationPrompt` |
| `internal/subagent/registry.go` | `GetAgentsSection` |
| `internal/subagent/executor_prompt.go` | `buildBrief` for `WithSubagentIdentity` |
| `internal/agent/build.go` | `BuildParams.IdentityText` and other prompt knobs |
| `internal/command/builtin/identity-{create,edit}.md` | Embedded workflow templates |

## Sample prompts

### Main agent (git repo, with skills + agents)

```text
You are Gen Code, an interactive AI assistant ...

# Tone / Output / Behavior / Scope / Code conventions
...

<policy>
Defensive security only ...
Reversibility and blast radius: ...
Authorization scope: ...
Root cause, not shortcut: ...
External input: ...
</policy>

<guidelines name="tools">
Prefer specialized tools over Bash ...
When you do spawn an Agent, brief it like a smart colleague ...
</guidelines>

<guidelines name="tasks">...</guidelines>
<guidelines name="questions">...</guidelines>
<guidelines name="git">...</guidelines>

<memory scope="user">
Always use tabs for indentation.
</memory>

<memory scope="project">
This is a Go project using Bubble Tea.
</memory>

<skills>
- git: Git workflow automation
- review: Review a pull request
</skills>

<agents>
- general-purpose: General multi-step agent
- code-reviewer: Reviews code changes without mutating the workspace
</agents>

<environment>
date: 2026-05-04
cwd: /Users/myan/Workspace/ideas/gencode
git: yes
platform: darwin/arm64
model: claude-sonnet-4-20250514
</environment>
```

### Subagent (`code-reviewer`, explore mode)

```text
<identity mode="explore">
You are a code-reviewer subagent operating inside Gen Code.
Role: Reviews code changes for bugs, security, performance, and style.

Operational scope: read-only research; do not modify files or run shell commands.
Tool constraints: Bash limited to git diff*, git log*, git show*, git status*

{AGENT.md body}
</identity>

<policy>... (same as main) ...</policy>

<guidelines name="tools">...</guidelines>
<guidelines name="git">...</guidelines>
<!-- no <guidelines name="tasks"> or <guidelines name="questions"> -->

<memory scope="user">...</memory>
<memory scope="project">...</memory>

<skills>... (filtered to those reachable from this agent's tools) ...</skills>
<!-- no <agents> — subagents do not recursively spawn -->

<environment>...</environment>
```

## See also

- [`subagent.md`](subagent.md) — subagent execution flow
- [`skill-system.md`](skill-system.md) — skill registry and invocation tool
- [`features/10-agents.md`](features/10-agents.md) — agent definition and lifecycle
- [`features/16-memory.md`](features/16-memory.md) — memory file conventions
