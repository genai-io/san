# Personas — switchable system prompt + skills + config

A **persona** is a single on-disk folder that bundles everything that makes San
behave a certain way — its system prompt, its skills, and its config overrides —
so the user can switch the whole bundle in one command, mid-session, without
restarting.

This unifies two concepts that are independent today (`identity` and `skill`),
and simplifies the system prompt down to a few replaceable parts.

**Contents.** [§0 Motivation](#0-motivation) · [§1 Concept](#1-concept-one-folder-one-persona) · [§2 First principles: the system prompt](#2-first-principles-the-system-prompt) · [§3 On-disk layout](#3-on-disk-layout) · [§4 settings.json overlay](#4-settingsjson-the-config-overlay) · [§5 Switch flow](#5-switch-flow) · [§6 Relationship to identity & skills](#6-relationship-to-identity--skills) · [§7 Current architecture](#7-current-architecture-what-we-build-on) · [§8 Decisions](#8-decisions-locked) · [§9 Phasing](#9-phasing) · [§10 Open questions](#10-open-questions)

---

## 0. Motivation

Today, "who San is" is split across two unrelated mechanisms, and the system
prompt's customizable surface is scattered:

| Concept | Where it lives | How you switch it | What it covers |
|---|---|---|---|
| `identity` | `~/.san/identities/<name>.md` (single file) | `/identity` | only the "You are X" preamble |
| `skill` | `.san/skills/<name>/SKILL.md` (global) | `/skills`, cycled one-by-one, state in `skills.json` | global on/off, unrelated to identity |

Problems:

- **No bundling.** A "research assistant" persona means *both* a research-flavored
  prompt *and* a research skill set — but those are configured in two places with
  two commands and no link between them.
- **Scattered prompt surface.** The system prompt is assembled from ~10 embedded
  files across 5 slots; there is no simple "here is the prompt, swap it" unit.
- **Skills are global.** Activating a skill for one task pollutes the global state
  for every other context.

**Goal:** one persona = one folder. Switching it swaps the system prompt, the
active skill set, and the config overlay (tools / permissions) as a unit — reusing
the hot-patch machinery San already has, so the session never restarts.

---

## 1. Concept: one folder, one persona

```
.san/personas/ml-researcher/
├── system/                 ← the system prompt, split into a few replaceable parts
│   ├── identity.md
│   ├── behavior.md
│   └── rules.md
├── skills/                 ← persona-scoped skill bundle (reuses the skill loader)
│   ├── lit-review/SKILL.md
│   └── run-experiment/SKILL.md
└── settings.json           ← config overlay: skills states, tools, permissions, …
```

A persona is **three orthogonal layers**, each optional:

1. **System prompt** — files under `system/` override the corresponding default
   parts (§2). Provide only the parts you want to change.
2. **Skills** — every skill under `skills/` is bundled with this persona and
   activated while it is selected (§5).
3. **Config** — `settings.json` overrides the user/project config for this
   persona (§4).

Everything is additive and fall-back-driven: a persona that only wants a different
voice ships a single `system/identity.md`; everything else uses San's defaults.

---

## 2. First principles: the system prompt

A system prompt only ever answers four questions:

| Question | Part | Content |
|---|---|---|
| Who am I? | `identity` | role / persona ("You are …") |
| How do I act? | `behavior` | communication style + engineering method |
| What rules do I follow? | `rules` | safety contract + tool / task / git protocols |
| Where/when am I? | `environment` | cwd / date / model / git (computed at runtime) |

That is the whole prompt. So the design is: **the system prompt is these four
parts, and each prose part is replaceable by a file. If the persona provides
`system/<part>.md`, use it; otherwise fall back to the built-in default.**
No special cases — not even safety (see the safety note below).

### Resolution (per part)

```
render part P  (identity | behavior | rules)
        │
        ▼
personas/<selected>/system/<P>.md exists?
        │                       │
       yes                      no
        │                       │
        ▼                       ▼
 use persona file        use built-in default (embedded)
        └───────────┬───────────┘
                    ▼
            append to system prompt
```

`environment` is the one exception: it is *computed facts*, not prose, so it is
always built-in. (A persona may ship an optional `system/context.md` to *append*
static context, but it does not replace the live environment block.)

### Slot simplification

```
            Today (5 slots + 3 sub-sections)            Proposed (4 slots)
┌──────────────────────────────────────────┐   ┌────────────────────────────────────┐
│ SlotIdentity   ── identity                │   │ SlotIdentity     ← system/identity.md │
│                ── output                   │ → │ SlotBehavior     ← system/behavior.md │  output + engineering
│                ── engineering              │   │ SlotRules        ← system/rules.md    │  policy + guidelines (+ provider)
│ SlotProvider   ── provider                │   │ SlotEnvironment  (computed footer)    │
│ SlotPolicy     ── policy                  │   └────────────────────────────────────┘
│ SlotGuidelines ── tools/tasks/questions/… │
│ SlotEnvironment ── cwd/date               │
└──────────────────────────────────────────┘
```

Mapping from today's embedded files (`internal/core/system/prompts/`):

| New part | Built-in default sourced from | Scope rules preserved |
|---|---|---|
| `identity` | `identity.txt` | — |
| `behavior` | `output.txt` + `engineering.txt` | main-only (subagents carry their own charter) |
| `rules` | `policy.txt` + `guidelines/{tools,system-reminders,tasks,questions,git}.txt` + provider quirks | `tasks`/`questions` main-only; `git` only when `isGit`; `policy`+`tools`+`system-reminders` always |
| `environment` | computed (`renderEnvironment`) | — |

The default `rules` renderer stays scope-aware and git-conditional (exactly as the
separate guidelines sections are today); merging them into one part changes the
*override granularity*, not the *default content*.

### Why `behavior` and `rules` (naming)

`behavior` = what the persona does of its own accord (style, working habits);
`rules` = constraints imposed on it (safety, protocols). The two earlier
candidate names (`conduct`, `guidelines`) both read as "rules of behavior" and
blurred this self-vs-imposed distinction. `identity / behavior / rules /
environment` reads as *who / how / what-rules / where*.

### Safety note: why "all parts replaceable" is safe

`rules` (which contains the safety contract) is replaceable like any other part —
a persona *could* ship a `rules.md` that drops the safety language. This is
acceptable because of **defense in depth**:

> The system prompt is *advisory* — guidance the model reads. The *enforcement*
> lives in the permission engine (the `settings.json` overlay, §4, where `deny`
> rules only ever accumulate). Even a persona whose `rules.md` removes all safety
> prose cannot grant itself a tool permission the settings layer denies.

Personas are local, user-authored files — same trust level as the user. Giving the
user full authority over the prose their own persona shows the model is correct;
the hard floor stays in the permission layer and in `managed-settings.json`.

---

## 3. On-disk layout

Personas are scanned from two roots, project overriding user on name collision —
mirroring how `identity` and `skill` already resolve scope:

```
~/.san/personas/<name>/      ← user level
.san/personas/<name>/        ← project level (overrides user)
```

```
<name>/
├── system/
│   ├── identity.md          ← optional; overrides the identity part
│   ├── behavior.md          ← optional; overrides the behavior part
│   └── rules.md             ← optional; overrides the rules part
├── skills/
│   └── <skill>/SKILL.md     ← persona-scoped skills (standard skill layout)
└── settings.json            ← optional; config overlay + persona metadata
```

Notes:

- All files are optional. The minimum useful persona is a single
  `system/identity.md`.
- The persona **name** is the directory name. `description` (for the selector) lives
  in `settings.json`; `system/*.md` files are pure prompt bodies (no frontmatter).
- `skills/` uses the existing skill loader unchanged — it already scans
  `<scope>/skills/<name>/SKILL.md`.

---

## 4. settings.json: the config overlay

A persona's `settings.json` is a **`setting.Data` plus a skills-state map**, applied
as the **highest file-level overlay** through the existing merger.

```json
{
  "description": "ML research specialist (PyTorch/JAX, experiment-driven)",
  "skills": {
    "lit-review": "active",
    "run-experiment": "active",
    "git:commit": "enable",
    "deep-research": "disable"
  },
  "disabledTools": { "WebSearch": false, "SomeHeavyTool": true },
  "permissions": {
    "defaultMode": "acceptEdits",
    "allow": ["Bash(pytest:*)", "Bash(python:*)"],
    "deny":  ["Bash(rm -rf:*)"]
  }
}
```

### Where it sits in the precedence chain

The existing load order (lowest → highest, see `internal/setting/settings.go`):

```
~/.claude → ~/.san → .claude → .san → *.local.json → [PERSONA] → env/CLI → managed
                                                          ▲
                                          new overlay: overrides all config files,
                                          below explicit CLI args and managed (immutable)
```

### Merge semantics (via `internal/setting/merger.go`)

| Field | Merge | What a persona can do |
|---|---|---|
| `skills` (new) | per-key override | **full override**: listed skills take the persona's state; unlisted keep the lower layer |
| `disabledTools` | per-key override (`mergeMaps`) | **full override**: can disable, and can re-enable a tool a lower layer disabled (`false`) |
| `permissions.defaultMode` | `coalesce` | persona wins if set |
| `permissions.allow/deny/ask` | union (`mergeStringSlices`) | **add only** — can tighten with new `deny`; **cannot remove** a lower-layer rule |
| `model` / `env` / `theme` / … | existing coalesce/merge | overridable for free |

The one asymmetry is deliberate and safety-biased: **a persona can tighten
permissions but not loosen them.** It cannot silently remove a project-level
`deny`. (`managed-settings.json` remains above personas and is never overridable.)

---

## 5. Switch flow

`/persona ml-researcher` (or `/persona` to open a selector). Two tiers of effect —
prompt and skills apply immediately; tools and permissions apply on the next agent
rebuild.

```
/persona ml-researcher
   │
   ├─(1) persist choice + recompute settings:  base chain ⊕ persona overlay
   │
   ├─(2) prompt   — SwapPersona(sys): replace identity/behavior/rules parts        ┐ immediate
   │                (each part: persona file if present, else default)             │
   ├─(3) skills   — reset registry in-memory state from settings.skills;           │ (hot-patch,
   │                RequeueSystemReminders() re-renders the skills directory        ┘ next inference)
   │
   ├─(4) tools        — effective DisabledTools changes                            ┐ next agent rebuild
   └─(5) permissions  — effective allow/deny/mode changes                          ┘ (/clear, new session)
```

| Dimension | When it takes effect | Mechanism |
|---|---|---|
| system prompt | immediate | `SwapPersona(sys)` → replaces parts by name (reuses `SwapIdentity` pattern) |
| skills (in-memory) | immediate | registry overlay + `RequeueSystemReminders()` |
| tools / permissions | next agent rebuild | recomputed overlay read at the next `buildAgent` |

The UI tells the user which parts went live and which await a rebuild, so a
config change never looks like it "did nothing".

### The four supporting flows

**Discovery & load** — on startup, cwd change, or switch:
```
persona.Registry.Reload(cwd)
  ├─ scan ~/.san/personas/*/      (user)
  └─ scan <cwd>/.san/personas/*/  (project, overrides user)
        → per dir: parse system/*.md, skills/*/SKILL.md, settings.json
        → Registry: map[name]*Persona
```

**Startup resolution** — pick the active persona and build:
```
settings.persona (project > user; empty = default)
  → BuildParams{ PersonaFiles, PersonaSkills, overlay settings }
  → system.Build(ScopeMain, WithPersona(p), …)
  → skill.Registry.LoadPersonaSkills(p)   (in-memory active)
```

**Inference assembly** — what the model sees each turn:
```
System   = sys.Prompt()                 ← cached; persona parts + defaults
Messages = … + <system-reminder source="skills-directory">  ← persona's active skills
```
Skills ride on the user message (not the system prompt) to protect the prompt-cache
prefix — switching skills never invalidates the cached system prompt.

---

## 6. Relationship to identity & skills

**Persona absorbs identity.** An `identity` becomes a degenerate persona: only a
`system/identity.md`, no skills, no overlay. Concretely:

- `internal/persona/` takes over the dual-root scan that `internal/identity/` does today.
- `settings.identity` → `settings.persona` (the old field is still read for back-compat).
- Existing `~/.san/identities/*.md` single files are recognized as skill-less personas
  — no user migration required.
- `/identity` stays as a deprecated alias for `/persona` for a release or two.

**Skills are reused, not replaced.** The persona `skills/` directory is loaded by
the existing skill loader. The only addition is an **in-memory active overlay** that
the persona owns (per the decision in §8): selecting a persona marks its skills
`active`; deselecting clears them. Persona skill state is **not** written to
`skills.json`, so it never collides with the user's global skill toggles.

---

## 7. Current architecture (what we build on)

This design is **additive** — it reuses existing extension points and does not
touch the agent run loop, the LLM interface, or session persistence.

| Area | File / symbol | Reused for |
|---|---|---|
| System prompt build | `internal/core/system/builder.go` `Build(scope, opts…)` | add `WithPersona` option |
| Slots & sections | `internal/core/section.go`, `internal/core/system/catalog.go` | reduce to 4 parts |
| Prompt render/cache | `internal/core/system_impl.go` `Use/Drop/Refresh/Prompt` | per-part hot-swap |
| Hot-swap identity | `internal/app/model_actions.go` `setActiveIdentity` → `system.SwapIdentity` | generalize to `SwapPersona` |
| Active resolution | `internal/app/agent.go` `activeIdentityBody` | → `activePersona` |
| Build params | `internal/agent/build.go` `BuildParams.IdentityText` | → persona parts |
| Identity registry | `internal/identity/registry.go` (dual-root scan) | template for `internal/persona/` |
| Skill registry | `internal/skill/registry.go` `GetActive`, `SetState`, `GetSkillsSection` | persona skill overlay |
| Skills reminder | `internal/app/agent.go` `ProviderSkillsDirectory`, `RequeueSystemReminders()` | re-render on switch |
| Settings + merge | `internal/setting/settings.go` `Data`, `internal/setting/merger.go` `mergeSettings` | persona overlay layer |
| Slash command | `internal/command/registry.go`, `internal/app/input/slash_command.go` `handleIdentityCommand` | `/persona` + selector |

---

## 8. Decisions (locked)

| # | Decision | Choice | Rationale |
|---|---|---|---|
| D1 | Directory & skills subdir | `~/.san/personas/` + `.san/personas/`, subdir `skills/` (no dot) | consistent with `.san/skills`, `.san/identities`; reuses skill loader's `<scope>/skills/` scan |
| D2 | Persona vs identity/skills | persona **absorbs** identity | one concept, one command; "simplify" |
| D3 | Persona skill state | in-memory, persona-lifetime (not `skills.json`) | clean switch; no pollution of global toggles |
| D4 | System prompt parts | 4: `identity` / `behavior` / `rules` / `environment` | first-principles who/how/what-rules/where |
| D5 | Part override | every prose part replaceable by file; missing file → default | uniform, no special cases |
| D6 | `settings.json` | reuse `setting.Data` + `skills` map; highest file overlay | free reuse of `merger.go` |
| D7 | tools/permissions timing | next agent rebuild (prompt + skills immediate) | low risk; avoids hot-swapping the live tool set |
| D8 | Permission override | `deny` is add-only (tighten, never loosen) | safety: a persona can't strip project-level denies |
| D9 | Naming | `behavior` + `rules` (not `conduct` / `guidelines`) | self-style vs imposed-rules, plain words |

---

## 9. Phasing

Ordered so each step is independently shippable and the early steps carry no
behavior change.

1. **Simplify the system prompt to 4 parts.** Reduce the `Slot` enum; merge
   `output`+`engineering` → `behavior`, `policy`+`guidelines`(+provider) → `rules`
   (scope-aware, git-conditional). Each prose part renders through a pure
   constructor taking an optional override body. Default output stays equivalent;
   update golden tests.
2. **`internal/persona/` package.** Dual-root scan; `Persona` struct (name, dir,
   resolved `system/*.md`, skill dirs, `settings.json`); `Registry` mirroring
   `internal/identity`.
3. **Wire the prompt override.** `WithPersona` build option + `SwapPersona`
   hot-patch, per-part file-or-default.
4. **`settings.json` overlay.** Persona layer in the merge chain; `skills` map +
   `Data` reuse.
5. **Persona-scoped skills.** `LoadPersonaSkills` / `ClearPersonaSkills`
   (in-memory active); reuse the skills-directory reminder.
6. **`/persona` command + hot-switch.** Selector (mirror `/identity`);
   `setActivePersona` doing the §5 five-step switch; reconfigure the subagent
   executor.
7. **Absorb identity (clean break).** `/identity` becomes a `/persona` alias;
   delete the identity selector, `internal/identity`, and all
   `settings.identity` / `identities/` back-compat.

---

## 10. Open questions

- **Optional `system/context.md`** for appended static context — include in v1 or
  defer? (Leaning defer; environment is enough to start.)
- **Subagent personas.** Should a subagent be able to run *as* a persona, or do
  subagents keep their own charter mechanism? (Current plan: charter unchanged;
  personas are a main-agent concept.)
- **`permissions.replace` escape hatch** to let a persona fully take over
  permissions (D8 is add-only). Deferred unless a real need appears.
- **Plugin-provided personas** — personas shipped inside a plugin, like plugin
  skills. Natural extension; not in the initial scope.
