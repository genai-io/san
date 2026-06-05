# Proposal: Identity Rework — Directory-based Prompt Configuration

## Summary

Redesign Identity as a **directory structure** under `~/.gen/identities/` (user)
and `.gen/identities/` (project). Each identity is a directory containing:
- `identity.md` — role configuration (frontmatter + preamble), the common case
- `prompts/` — **optional** persona-layer prompt overrides
- `skills/` — **optional** bundled skills, active only while this identity is

**Core mechanism: embedded default + file-based override.** Default prompt
content stays `//go:embed`'d in the binary. When an identity has a prompt file
on disk, it overrides the embedded default. When it doesn't, the embedded
default is used directly — no extraction to disk, no upgrade clobber.

Custom identities only need to contain the **differences** from default.

The first new built-in identity: **`readonly`**, with minimal prompts and
read-only tools.

**Locked layers.** Identity can change persona (preamble, output style, tool
permissions, bundled skills), but policy and core guidelines are
harness-enforced and **never droppable** — they are not listed in `sections:`
and are always injected.

## Directory structure

```
~/.gen/identities/
│
├── default/                        ← default identity (built-in, embedded in binary)
│   └── identity.md                 ← one file, common case
│
├── readonly/                       ← read-only identity (built-in, embedded in binary)
│   └── identity.md
│
└── code-reviewer/                  ← user-defined identity with overrides
    ├── identity.md                 ← frontmatter + preamble
    ├── prompts/                    ← OPTIONAL persona-layer overrides
    │   └── output.txt              ← only overrides output; rest uses embedded default
    └── skills/                     ← OPTIONAL bundled skills
        └── lint-rules/
            └── SKILL.md

.gen/identities/                    ← project-level (overrides user-level on name collision)
└── project-role/
    └── identity.md
```

### Compared to current structure

```
Current:                                New:
~/.gen/identities/                      ~/.gen/identities/
├── README.md                           ├── default/
└── ml-engineer.md    ← single .md file │   └── identity.md       ← frontmatter + preamble
                                        ├── readonly/
                                        │   └── identity.md
                                        ├── code-reviewer/
                                        │   ├── identity.md
                                        │   ├── prompts/
                                        │   │   └── output.txt
                                        │   └── skills/
                                        │       └── lint-rules/SKILL.md
                                        └── old-style.md          ← migrated, body→preamble
```

## Identity resolution: global + project

`identity/registry.go` already resolves both scopes today. Keep both —
project wins on name collision:

```
/identity foo

  1. .gen/identities/foo/     → found? use project
  2. ~/.gen/identities/foo/   → found? use user
  3. neither                  → use default
```

This preserves the existing behavior where project-level config can
override user-level config.

## Fallback mechanism

This is the core of the design. **Defaults stay embedded** in the binary
via `//go:embed`. The fallback chain is:

```
Loading the "output" section for identity "code-reviewer":

  1. ~/.gen/identities/code-reviewer/prompts/output.txt   ← identity's override file
                                        doesn't exist? ↓
  2. embedded default output.txt                           ← go:embed default (always exists)

No extraction to disk. No upgrade clobber.
```

```go
// Default prompts are embedded in the binary, never extracted.
// An identity only has files on disk for what it explicitly overrides.
//
//go:embed prompts/*
var defaultPrompts embed.FS

func resolvePrompt(identityDir string, sectionName string) string {
    // 1. Look for the identity's own override file on disk
    p := filepath.Join(identityDir, "prompts", sectionName+".txt")
    if content, ok := readFile(p); ok {
        return content
    }
    // 2. Fall back to embedded default (always exists, no disk I/O needed)
    content, _ := defaultPrompts.ReadFile("prompts/" + sectionName + ".txt")
    return string(content)
}
```

### What this means

**default identity**: no `prompts/` directory on disk → all sections use
embedded defaults directly (equivalent to current behavior, no disk copy)

**readonly identity**: `identity.md` declares only `environment` in sections
→ only environment is loaded; policy and core guidelines are always injected
by the harness regardless

**Custom identity**: only needs prompt files that **differ from default**.
For example, to just change the output style:

```
code-reviewer/
├── identity.md             ← frontmatter: sections, tools
│                             body: preamble
└── prompts/
    └── output.txt          ← its own output; all else uses embedded default
```

## Locked layers vs persona layers

Identity changes are scoped to the **persona layer**. Policy and core
guidelines are harness-enforced and cannot be dropped:

```
┌─────────────────────────────────────────────┐
│ IDENTITY CAN CHANGE (persona layer)         │
│                                             │
│  • preamble (identity declaration)          │
│  • output / engineering style               │
│  • tool permissions (integrated with        │
│    existing permission layer)               │
│  • bundled skills                           │
└─────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│ HARNESS-ENFORCED — NEVER DROPPABLE          │
│                                             │
│  • policy (safety contract)                 │
│  • core guidelines (tool usage, reminders,  │
│    task workflow)                           │
└─────────────────────────────────────────────┘
```

This means:
- `sections:` in identity config only lists **persona-layer** sections
  (`output`, `engineering`, `environment`)
- `policy` and `guidelines` are never in `sections:` — they are always
  injected by the harness after persona sections
- An identity cannot accidentally (or intentionally) remove the safety
  contract

## identity.md format

Single file with YAML frontmatter + markdown body. The body is the preamble.

### default/identity.md

```markdown
---
name: default
description: Built-in Gen Code persona — software engineering generalist
sections:
  - output
  - engineering
  - environment
---

You are Gen Code, an interactive AI assistant for software
engineering tasks running in a terminal.
```

### readonly/identity.md

```markdown
---
name: readonly
description: Read-only assistant — search, analyze, answer questions
sections:
  - environment
---

You are an AI assistant. You can read files, search code, and answer
questions. You cannot modify files or execute commands.
```

### code-reviewer/identity.md (user-defined with overrides)

```markdown
---
name: code-reviewer
description: Code review specialist — read-only, focused on logic and style
sections:
  - engineering
  - environment
---

You are a code review specialist. Carefully read code to find bugs,
security vulnerabilities, performance issues, and style inconsistencies.
```

Together with `code-reviewer/prompts/output.txt`:

```
<output>
Be thorough. Cite file paths and line numbers. Suggest fixes.
</output>
```

Other sections (`engineering`, `environment`) use embedded defaults.
`policy` and `guidelines` are always injected by the harness.

### my-custom/identity.md (user-defined, overrides output)

```markdown
---
name: my-custom
description: My custom role — concise output + engineering standards
sections:
  - output
  - engineering
  - environment
---

You are a concise coding assistant. One sentence per response, max.
```

Together with `my-custom/prompts/output.txt`:

```
<output>
Always concise. Use bullet points. Never apologize.
</output>
```

## Bundling skills

Skills are already directory-based, loaded by `skill/loader.go` with
multiple search scopes. The active identity's `skills/` directory is
added as an additional search root — live only while the identity is active:

```
Skill loader search paths (when identity "code-reviewer" is active):

  1. identities/code-reviewer/skills/   ← NEW: identity-bundled, active only while this identity is
  2. ~/.gen/skills/                     ← user-installed skills
  3. .gen/skills/                       ← project skills
  4. built-in skills                    ← shipped with the binary
```

This makes identities self-contained and shareable:

```bash
# Export an identity with its skills
tar -czf code-reviewer.tar.gz -C ~/.gen/identities code-reviewer/

# Import — everything comes along
tar -xzf code-reviewer.tar.gz -C ~/.gen/identities/
gen> /identity code-reviewer
```

Open choice: **bundle** skills (self-contained, shareable as a tarball)
vs **reference** by name (`skills: [git:commit]`, no duplication).
Default to bundle, allow reference.

## Tool permissions

Tools are scoped through the **existing permission layer**, not a parallel
`tools: allow/deny` system. The identity's tool constraints are merged into
the current permission configuration:

```markdown
---
name: readonly
description: Read-only assistant
sections:
  - environment
tools:
  allow:
    - Read
    - Grep
    - Glob
    - WebSearch
    - WebFetch
---
```

- Empty or omitted `tools:` → no restrictions (all tools available)
- `tools.allow:` → only these tools are available
- Tool permissions are **intersected** with the existing permission layer
  (e.g. if the harness already blocks `Bash`, identity can't re-enable it)

## Migration: flat `<name>.md` → directory

Existing flat `~/.gen/identities/<name>.md` files are migrated
non-breakingly:

```
Before:                                After:
~/.gen/identities/                     ~/.gen/identities/
└── ml-engineer.md                     └── ml-engineer/
    (frontmatter: name, sections           └── identity.md
     body: preamble)                           (same file, body → preamble)
```

Migration logic:
1. Scan `~/.gen/identities/` for `.md` files that are NOT inside
   subdirectories
2. For each: create `<name>/` directory, move file to `<name>/identity.md`
3. Body of original `.md` becomes the preamble (markdown body of
   `identity.md`)

This is run automatically on first startup after the upgrade.

## Final system prompt assembly

```
default identity → sections: [output, engineering, environment]
  + harness-injected: policy, guidelines

  You are Gen Code, ...                                    ← preamble (from identity.md body)
  <output>                                                 ← embedded default (go:embed)
  ...
  </output>
  <engineering>                                            ← embedded default
  ...
  </engineering>
  <environment>                                            ← embedded default
  date: 2026-06-05  cwd: /project  platform: darwin/arm64
  </environment>
  <policy>                                                 ← HARNESS: always injected
  ...
  </policy>
  <guidelines name="tool-usage">...</guidelines>           ← HARNESS: always injected
  <guidelines name="system-reminders">...</guidelines>
  ...

readonly identity → sections: [environment]
  + harness-injected: policy, guidelines

  You are an AI assistant. ...                             ← preamble
  <environment>                                            ← embedded default
  date: 2026-06-05  cwd: /project  platform: darwin/arm64
  </environment>
  <policy>                                                 ← HARNESS: always injected
  ...
  </policy>
  <guidelines>                                             ← HARNESS: always injected
  ...
  </guidelines>

code-reviewer identity → sections: [engineering, environment]
  + harness-injected: policy, guidelines

  You are a code review specialist. ...                    ← preamble
  <output>                                                 ← code-reviewer/prompts/output.txt (override)
  Be thorough. Cite file paths and line numbers.
  </output>
  <engineering>                                            ← embedded default (no override file)
  ...
  </engineering>
  <environment>                                            ← embedded default
  ...
  </environment>
  <policy>                                                 ← HARNESS: always injected
  ...
  </policy>
  <guidelines>                                             ← HARNESS: always injected
  ...
  </guidelines>
```

## Initialization

No disk extraction needed for built-in identities. Defaults stay embedded.
Only user-created identities exist on disk:

```go
func Initialize(cwd string) {
    // Migrate flat .md files to directory structure (one-time)
    migrateFlatIdentityFiles()

    // Ensure identity directories exist
    os.MkdirAll(filepath.Join(userIdentitiesDir), 0755)
    // Note: default & readonly are embedded, no disk extraction needed
}

// migrateFlatIdentityFiles migrates existing ~/.gen/identities/<name>.md
// files into ~/.gen/identities/<name>/identity.md directories.
func migrateFlatIdentityFiles() {
    entries, _ := os.ReadDir(userIdentitiesDir)
    for _, e := range entries {
        if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
            continue
        }
        name := strings.TrimSuffix(e.Name(), ".md")
        dir := filepath.Join(userIdentitiesDir, name)
        os.MkdirAll(dir, 0755)
        os.Rename(
            filepath.Join(userIdentitiesDir, e.Name()),
            filepath.Join(dir, "identity.md"),
        )
    }
}
```

Key behaviors:
- **Embedded defaults are never extracted to disk** — no upgrade clobber
- **User overrides only exist on disk when explicitly created**
- **Flat `.md` files are migrated one-time** on first startup after upgrade
- **User-created identity directories are unaffected by init/upgrade**

## User experience

### Creating a custom identity

```bash
# 1. Create directory
mkdir -p ~/.gen/identities/concise

# 2. Write identity.md
cat > ~/.gen/identities/concise/identity.md << 'EOF'
---
name: concise
description: Concise mode
sections:
  - engineering
  - environment
---

You are a concise, direct coding assistant. Get to the point.
EOF

# 3. (Optional) Override a section's prompt
mkdir -p ~/.gen/identities/concise/prompts
cat > ~/.gen/identities/concise/prompts/output.txt << 'EOF'
<output>
Answer directly. No small talk. No apologies.
</output>
EOF

# 4. (Optional) Bundle skills
mkdir -p ~/.gen/identities/concise/skills/short-answers
cat > ~/.gen/identities/concise/skills/short-answers/SKILL.md << 'EOF'
# Short Answers
Always answer in 3 lines or fewer.
EOF

# 5. Switch to the identity
gen> /identity concise
✓ Identity switched: concise
```

### Creating a project-level identity

```bash
# Lives in the repo, can be committed
mkdir -p .gen/identities/project-role

cat > .gen/identities/project-role/identity.md << 'EOF'
---
name: project-role
description: Project-specific conventions
sections:
  - engineering
  - environment
---

You are working on the Gen Code project. Follow Go conventions.
EOF
```

### Deleting a custom identity

```bash
rm -rf ~/.gen/identities/my-custom
# Completely removed; other identities unaffected
```

## Identity data structure

```go
type Identity struct {
    Name        string   // Directory name, also the identity name
    Description string   // One-liner
    Preamble    string   // Identity declaration (body of identity.md)
    Sections    []string // Persona-layer section names to load, in order
    Tools       ToolPolicy // Tool restrictions (merged with permission layer)
    Dir         string   // Path to this identity's directory (empty for embedded default)
    SkillsDir   string   // Path to bundled skills/ (empty if none)
}
```

## Extensibility

### Adding a new built-in identity

1. Add `identity.md` content to embedded FS (`internal/identity/builtin/`)
2. Register in the built-in identity list
3. Done. No disk extraction needed

### Adding a new persona-layer section type

1. Add a `.txt` file to the embedded default prompts
2. Add one line to the section→slot mapping table
3. Existing identities that declare the new section in their list will
   automatically load it
4. Identities that don't declare it are unaffected

### Community sharing

```bash
# Export an identity (directory tarball — includes skills if bundled)
tar -czf my-architect.tar.gz -C ~/.gen/identities architect/

# Import
tar -xzf my-architect.tar.gz -C ~/.gen/identities/
gen> /identity architect
```

## Relationship with existing system

| Existing concept | Change |
|---|---|
| `~/.gen/identities/*.md` | Flat `.md` files migrated to `<name>/identity.md` directories |
| `prompts/*.txt` (embedded) | Default prompts stay `//go:embed`'d, never extracted |
| `.gen/identities/` (project) | **Kept** — project wins on name collision |
| `applyDefaults()` | Hardcoded scope branches replaced by iterating over `Identity.Sections` |
| `core.Scope` | Keep Main/Subagent concept, but persona-layer composition is identity-driven |
| Policy & guidelines | **Harness-enforced** — always injected, never droppable |
| `/identity` | Scans both `.gen/identities/` and `~/.gen/identities/` |
| `settings.json` | `"identity": "readonly"` points to the directory name |
| Skill loader | Active identity's `skills/` added as a search root |
| Tool permissions | Merged into existing permission layer, not a parallel system |

## Default prompts content

Files embedded in the binary under `prompts/` match the current
`prompts/*.txt` content:

| File | Corresponding Slot | Layer | Description |
|---|---|---|---|
| `output.txt` | SlotIdentity | Persona | Tone, updates, behavior |
| `engineering.txt` | SlotIdentity | Persona | Restraint, code conventions, error handling |
| `environment.txt` | SlotEnvironment | Persona | Environment template (with `{{.Date}}` etc. variables) |
| `policy.txt` | SlotPolicy | **Locked** | Safety contract (harness-enforced) |
| `guidelines.txt` | SlotGuidelines | **Locked** | Tool usage, system reminders, task workflow (harness-enforced) |

## Decided questions

1. **Project-level identity directories?** → **Keep both.** `.gen/identities/`
   (project) and `~/.gen/identities/` (user). Project wins on name collision.
   `identity/registry.go` already resolves both today.

2. **Can the default identity be deleted?** → **Not applicable.** Default
   identity is embedded in the binary, not extracted to disk. There is no
   `~/.gen/identities/default/` directory to delete.

3. **Should upgrades update default prompts?** → **Yes, automatically.**
   Default prompts are `//go:embed`'d. Upgrading the binary upgrades the
   defaults — no disk extraction, no clobber risk, no user data loss.
   User overrides in custom identities are unaffected.

4. **Can an identity remove the safety policy?** → **No.** `policy` and
   `guidelines` are harness-enforced locked layers. They are never listed
   in `sections:` and are always injected regardless of identity.

5. **How are existing flat `.md` identities handled?** → **One-time migration.**
   On first startup after upgrade, `~/.gen/identities/<name>.md` is moved to
   `~/.gen/identities/<name>/identity.md`. Non-breaking.

## References

- [identity.go](../../internal/identity/identity.go) — Current Identity struct
- [catalog.go](../../internal/core/system/catalog.go) — Current system prompt assembly
- [section.go](../../internal/core/section.go) — Section and Slot types
- [prompts/](../../internal/core/system/prompts/) — Currently embedded prompt files (stay embedded, not extracted)
