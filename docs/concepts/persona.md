# Personas — switchable system prompt + skills + config

A **persona** is a single on-disk folder that bundles everything that makes San
behave a certain way — its system prompt, its skills, and its config overrides —
so you can switch the whole bundle with one command, mid-session, without
restarting.

Personas are San's single mechanism for "who San is": they unify the system
prompt and the active skill set behind one switch, and reduce the system prompt
to a few replaceable parts.

**Contents.** [§1 Concept](#1-concept-one-folder-one-persona) · [§2 The system prompt](#2-the-system-prompt-four-parts) · [§3 On-disk layout](#3-on-disk-layout) · [§4 settings.json overlay](#4-settingsjson-the-config-overlay) · [§5 Switch flow](#5-switch-flow) · [§6 Relationship to skills](#6-relationship-to-skills) · [§7 Design decisions](#7-design-decisions) · [§8 Future directions](#8-future-directions)

---

## Why personas

A useful working mode is rarely *just* a prompt or *just* a skill set. A
"research assistant" wants both a research-flavored prompt **and** a research
skill set, plus the right tools and permissions. Before personas these lived in
separate places with separate switches and no link between them, and skill
activation was global — turning a skill on for one task leaked into every other
context.

A persona bundles all of it into one folder and one switch: selecting it swaps
the system prompt, the active skill set, and the config overlay (tools /
permissions) as a unit, reusing the hot-patch machinery San already has so the
session never restarts.

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

## 2. The system prompt: four parts

A system prompt only ever answers four questions:

| Question | Part | Content |
|---|---|---|
| Who am I? | `identity` | role / persona ("You are …") |
| How do I act? | `behavior` | communication style + engineering method |
| What rules do I follow? | `rules` | safety contract + harness protocols |
| Where/when am I? | `environment` | cwd / date / branch / platform (computed at runtime) |

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
always built-in and cannot be replaced.

Each prose part's built-in default is composed from the embedded prompt files
under `internal/core/system/prompts/`:

| Part | Built-in default sourced from | Scope rules |
|---|---|---|
| `identity` | `identity.txt` | — |
| `behavior` | `behavior.txt` | main-only (subagents carry their own charter) |
| `rules` | `rules.txt` | always |
| `environment` | computed (`renderEnvironment`) | — |

A persona that overrides `rules.md` replaces that whole part.

### Why `behavior` and `rules` (naming)

`behavior` = what the persona does of its own accord (style, working habits);
`rules` = constraints imposed on it (safety, protocols). `identity / behavior /
rules / environment` reads as *who / how / what-rules / where*.

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
mirroring how skills already resolve scope:

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

A persona's `settings.json` is a **`setting.Data` config overlay plus two
persona-scoped selections — a skills-state map and a subagent allow-list**. The
overlay is applied as the **highest file-level layer** through the existing
merger; the two selections apply in-memory while the persona is active.

```json
{
  "description": "ML research specialist (PyTorch/JAX, experiment-driven)",
  "skills": {
    "lit-review": "active",
    "run-experiment": "active",
    "git:commit": "enable",
    "deep-research": "disable"
  },
  "agents": ["test-runner", "project-reviewer"],
  "disabledTools": { "WebSearch": false, "SomeHeavyTool": true },
  "permissions": {
    "defaultMode": "acceptEdits",
    "allow": ["Bash(pytest:*)", "Bash(python:*)"],
    "deny":  ["Bash(rm -rf:*)"]
  }
}
```

### Where it sits in the precedence chain

The load order (lowest → highest, see `internal/setting/settings.go`):

```
~/.claude → ~/.san → .claude → .san → *.local.json → [PERSONA] → env/CLI → managed
                                                          ▲
                                          persona overlay: overrides all config files,
                                          below explicit CLI args and managed (immutable)
```

### Merge semantics (via `internal/setting/merger.go`)

| Field | Merge | What a persona can do |
|---|---|---|
| `skills` | per-key override | **full override**: listed skills take the persona's state; unlisted keep the lower layer |
| `agents` | allow-list (replace) | **restrict visibility**: when non-empty, only the listed subagents are spawnable/shown; empty/omitted = all visible |
| `disabledTools` | per-key override (`mergeMaps`) | **full override**: can disable, and can re-enable a tool a lower layer disabled (`false`) |
| `permissions.defaultMode` | `coalesce` | persona wins if set |
| `permissions.allow/deny/ask` | union (`mergeStringSlices`) | **add only** — can tighten with new `deny`; **cannot remove** a lower-layer rule |
| `model` / `env` / `theme` / … | existing coalesce/merge | overridable for free |

The one asymmetry is deliberate and safety-biased: **a persona can tighten
permissions but not loosen them.** It cannot silently remove a project-level
`deny`. (`managed-settings.json` remains above personas and is never overridable.)

`skills` and `agents` are **persona-scoped selections applied in-memory** while
the persona is active — they are not part of the layered config merge and are
never written to `skills.json` or the agent enable/disable stores. The `agents`
allow-list composes with those stores: an agent must be on the allow-list *and*
not disabled to be visible.

The overlay also ignores any `persona` selector inside a persona's own
`settings.json` — a persona cannot re-select the active persona, which would be
circular.

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
| system prompt | immediate | `SwapPersona(sys)` → replaces parts by name |
| skills (in-memory) | immediate | registry overlay + `RequeueSystemReminders()` |
| subagent allow-list | enforcement immediate; prompt listing next rebuild | registry allow-list gates spawns live; the agents directory re-renders on the next `buildAgent` |
| tools / permissions | next agent rebuild | recomputed overlay read at the next `buildAgent` |

The UI tells the user which parts went live and which await a rebuild, so a
config change never looks like it "did nothing".

### The supporting flows

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
  → BuildParams{ persona parts, persona skills, overlay settings }
  → system.Build(ScopeMain, WithPersona(p), …)
  → skill.Registry.LoadPersona(p)   (in-memory active)
```

**Inference assembly** — what the model sees each turn:
```
System   = sys.Prompt()                 ← cached; persona parts + defaults
Messages = … + <system-reminder source="skills-directory">  ← persona's active skills
```
Skills ride on the user message (not the system prompt) to protect the prompt-cache
prefix — switching skills never invalidates the cached system prompt.

---

## 6. Relationship to skills

Skills are **reused, not replaced**. A persona's `skills/` directory is loaded by
the existing skill loader. The only addition is an **in-memory active overlay**
that the persona owns: selecting a persona marks its skills `active`; deselecting
clears them. Persona skill state is **not** written to `skills.json`, so it never
collides with the user's global skill toggles.

Personas replaced the former standalone `identity` mechanism (a single "You are
X" file). An identity is just a degenerate persona — only a `system/identity.md`,
no skills, no overlay — so nothing was lost in the merge. `/identity` remains as a
plain alias of `/persona`.

---

## 7. Design decisions

| # | Decision | Choice | Rationale |
|---|---|---|---|
| D1 | Directory & skills subdir | `~/.san/personas/` + `.san/personas/`, subdir `skills/` (no dot) | consistent with `.san/skills`; reuses the skill loader's `<scope>/skills/` scan |
| D2 | One "who San is" mechanism | persona, not a separate identity feature | one concept, one command |
| D3 | Persona skill state | in-memory, persona-lifetime (not `skills.json`) | clean switch; no pollution of global toggles |
| D4 | System prompt parts | 4: `identity` / `behavior` / `rules` / `environment` | first-principles who/how/what-rules/where |
| D5 | Part override | every prose part replaceable by file; missing file → default | uniform, no special cases |
| D6 | `settings.json` | reuse `setting.Data` overlay + `skills` map + `agents` allow-list | free reuse of `merger.go`; visibility selections stay in-memory |
| D7 | tools/permissions timing | next agent rebuild (prompt + skills immediate) | low risk; avoids hot-swapping the live tool set |
| D8 | Permission override | `deny` is add-only (tighten, never loosen) | safety: a persona can't strip project-level denies |
| D9 | Naming | `behavior` + `rules` (not `conduct` / `guidelines`) | self-style vs imposed-rules, plain words |

---

## 8. Future directions

Not yet supported; natural extensions if a need appears:

- **Optional `system/context.md`** for appended static context (distinct from the
  live, computed `environment` block).
- **Subagent personas** — let a subagent run *as* a persona. A persona already
  controls *which* subagents are visible (the `agents` allow-list, §4); this is
  the inverse — giving a spawned subagent a persona's prompt and skills. Today
  subagents keep their own charter mechanism; personas are a main-agent concept.
- **`permissions.replace` escape hatch** to let a persona fully take over
  permissions (D8 is add-only).
- **Plugin-provided personas** — personas shipped inside a plugin, like plugin
  skills.
