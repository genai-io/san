# Proposal: Identity Rework — Directory-based Prompt Configuration

## Summary

Redesign Identity as a **directory structure** under `~/.gen/identities/` (user)
and `.gen/identities/` (project). Each identity is a directory containing:
- `identity.md` — role configuration (frontmatter + preamble), the common case
- `prompts/` — **optional** persona-layer prompt overrides
- `skills/` — **optional** bundled skills, active only while this identity is

**Core mechanism: extracted defaults + file-based fallback.** Default prompt
content (output, engineering, guidelines, environment) is compiled into the
binary via `//go:embed` and **extracted to `default/prompts/` on disk**.
These files serve as the **reference** for users creating custom identities.
On every startup, the system verifies their integrity — if a user has modified
them, they are **overwritten** with the canonical version.

Custom identities only need to contain the **differences** from default.
When an identity is missing a prompt file, the system falls back to
`default/prompts/` on disk.

The first new built-in identity: **`readonly`**, with minimal prompts and
read-only tools.

**Locked layers.** Policy is harness-enforced and **never droppable** —
it is not listed in `sections:` and is always injected by the harness.

## `sections:` and `prompts/` — who controls what

There are two concerns when assembling the system prompt:

1. **Which** sections to load, and in what **order**
2. **What content** to use for each section

These are separate, and the design separates them explicitly:

| Concern | Controlled by | Description |
|---|---|---|
| Which sections + order | `sections:` in `identity.md` | **Single source of truth.** The complete, declarative list of persona-layer sections this identity loads, in order. |
| Content for each section | `prompts/` directory | **Override mechanism.** Files here provide custom content for sections declared in `sections:`. |

### The rules

1. **`sections:` is the complete list.** Only sections listed in `sections:` are loaded.
2. **`prompts/` only affects sections already in `sections:`.** A file in `prompts/` for a section NOT in `sections:` is **ignored** (with a warning).
3. **Content resolution for each section in `sections:`:**
   - Look for `<identity>/prompts/<section>.txt` → found? use it
   - Not found? → fall back to `default/prompts/<section>.txt`
   - Neither exists? → section is empty, not injected
4. **Policy is never in `sections:`** — it is always injected by the harness at the end.

### Why not use `prompts/` directory listing as the sections list?

Because then there is no way to express "load this section from default without
creating a file for it." Every identity would need a full copy of every section
it wants to load. And "readonly" couldn't express "only load environment" without
deleting files — but there are no files to delete since it uses fallback.

With `sections:` as the explicit list, `readonly` simply declares `[environment]`
and gets exactly that. A custom identity declares the full set it wants and only
creates override files for what differs.

### Examples

```
# code-reviewer: loads 4 sections, overrides 1
sections: [output, engineering, guidelines, environment]
prompts/
  └── output.txt          ← custom output content

# readonly: loads 1 section, no overrides
sections: [environment]
prompts/                  ← empty or absent; environment falls back to default

# concise: loads 4 sections, overrides 2
sections: [output, engineering, guidelines, environment]
prompts/
  ├── output.txt          ← custom output content
  └── engineering.txt     ← custom engineering content
  # guidelines, environment → fall back to default

# ANTI-PATTERN: guidelines.txt exists but guidelines not in sections
sections: [output, engineering, environment]
prompts/
  └── guidelines.txt      ← IGNORED (with warning): guidelines not in sections:
```

## Directory structure

```
~/.gen/identities/
│
├── default/                        ← default identity (built-in, written on init)
│   ├── identity.md                 ← frontmatter + preamble
│   └── prompts/                    ← full set of default prompts (REFERENCE)
│       ├── output.txt              ← extracted from binary; DO NOT MODIFY
│       ├── engineering.txt         ← extracted from binary; DO NOT MODIFY
│       ├── guidelines.txt          ← extracted from binary; DO NOT MODIFY
│       └── environment.txt         ← extracted from binary; DO NOT MODIFY
│
├── readonly/                       ← read-only identity (built-in, written on init)
│   ├── identity.md
│   └── prompts/                    ← mostly empty, everything falls back to default
│       └── environment.txt         ← optional; can also be omitted (fallback)
│
└── code-reviewer/                  ← user-defined identity with overrides
    ├── identity.md                 ← frontmatter + preamble
    ├── prompts/                    ← OPTIONAL persona-layer overrides
    │   └── output.txt              ← custom output; rest falls back to default
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
└── ml-engineer.md    ← single .md file │   ├── identity.md       ← frontmatter + preamble
                                        │   └── prompts/          ← reference copies for users
                                        │       ├── output.txt
                                        │       ├── engineering.txt
                                        │       ├── guidelines.txt
                                        │       └── environment.txt
                                        ├── readonly/
                                        │   ├── identity.md
                                        │   └── prompts/
                                        │       └── (empty, all fallback)
                                        ├── code-reviewer/
                                        │   ├── identity.md
                                        │   ├── prompts/
                                        │   │   └── output.txt
                                        │   └── skills/
                                        │       └── lint-rules/SKILL.md
                                        └── old-style.md          ← migrated, body→preamble
```

## Why defaults on disk?

Extracting defaults to `default/prompts/` serves a critical purpose:
**users need a reference to know what they can customize.** When creating
a custom identity, a user can look at `default/prompts/output.txt` to see
the current default output prompt, then decide what to change.

Without this, users would have to guess what sections exist, what the
default content looks like, and what format to use. The disk copy is the
source of truth that users can read — but not modify.

### Integrity enforcement

On **every startup**, the system verifies the integrity of
`default/prompts/`:

```go
func verifyDefaultPrompts() error {
    for _, filename := range builtin.PromptFiles {
        diskPath := filepath.Join(defaultIdentityDir, "prompts", filename)
        canonical := builtin.ReadPrompt(filename)
        onDisk, err := os.ReadFile(diskPath)

        if os.IsNotExist(err) {
            // Missing — restore
            os.WriteFile(diskPath, canonical, 0644)
            continue
        }

        if bytes.Equal(onDisk, canonical) {
            continue // Unchanged, OK
        }

        // User modified the file — overwrite with canonical version
        log.Warn("default prompt modified, restoring: %s", filename)
        os.WriteFile(diskPath, canonical, 0644)
    }
    return nil
}
```

Key behaviors:
- **Every startup**: integrity check runs, not just on init/upgrade
- **Modified? Overwritten.** User changes to `default/prompts/` are reverted
- **Missing? Restored.** Deleted files are re-extracted
- **User-created identities are never touched** — only `default/prompts/`
- **Policy is not in `default/prompts/`** — it is harness-enforced and
  injected directly by the system

Users who want to customize prompts should create their own identity
directory, not modify `default/prompts/`.

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

## Content resolution

For each section declared in `sections:`, content is resolved as follows:

```
For identity "code-reviewer" with sections: [output, engineering, guidelines, environment]

  output:
    1. code-reviewer/prompts/output.txt     → found! use it (CUSTOM)

  engineering:
    1. code-reviewer/prompts/engineering.txt → not found
    2. default/prompts/engineering.txt       → found! use it (FALLBACK)

  guidelines:
    1. code-reviewer/prompts/guidelines.txt  → not found
    2. default/prompts/guidelines.txt        → found! use it (FALLBACK)

  environment:
    1. code-reviewer/prompts/environment.txt → not found
    2. default/prompts/environment.txt       → found! use it (FALLBACK)

For identity "readonly" with sections: [environment]

  environment:
    1. readonly/prompts/environment.txt      → not found
    2. default/prompts/environment.txt       → found! use it (FALLBACK)

  Note: output, engineering, guidelines are NOT in sections: → not loaded at all.
  Even if readonly/prompts/output.txt existed, it would be ignored.
```

```go
func resolveSections(identity *Identity) []ResolvedSection {
    var resolved []ResolvedSection

    for _, sectionName := range identity.Sections {
        content := ""

        // 1. Look for the identity's own override file on disk
        p := filepath.Join(identity.Dir, "prompts", sectionName+".txt")
        if c, ok := readFile(p); ok {
            content = c
        } else {
            // 2. Fall back to default identity's same-named file on disk
            p = filepath.Join(defaultIdentityDir, "prompts", sectionName+".txt")
            if c, ok := readFile(p); ok {
                content = c
            }
        }

        if content != "" {
            resolved = append(resolved, ResolvedSection{
                Name:    sectionName,
                Content: content,
            })
        }
    }

    // 3. Warn about orphan files in prompts/ not declared in sections:
    warnOrphanFiles(identity.Dir, identity.Sections)

    return resolved
}

// warnOrphanFiles checks for files in prompts/ that are not in sections:
// and warns the user. These files are ignored.
func warnOrphanFiles(identityDir string, declaredSections []string) {
    promptsDir := filepath.Join(identityDir, "prompts")
    entries, _ := os.ReadDir(promptsDir)
    declared := setOf(declaredSections)
    for _, e := range entries {
        name := strings.TrimSuffix(e.Name(), ".txt")
        if !declared[name] {
            log.Warn("identity %s: prompts/%s ignored — section not declared in sections:",
                filepath.Base(identityDir), e.Name())
        }
    }
}
```

## Locked layers vs persona layers

Identity changes are scoped to the **persona layer**. Policy is
harness-enforced and cannot be dropped:

```
┌─────────────────────────────────────────────┐
│ IDENTITY CAN CHANGE (persona layer)         │
│                                             │
│  • preamble (identity declaration)          │
│  • output / engineering style               │
│  • guidelines (tool usage, reminders, etc.) │
│  • environment (date, cwd, platform)        │
│  • tool permissions (integrated with        │
│    existing permission layer)               │
│  • bundled skills                           │
└─────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│ HARNESS-ENFORCED — NEVER DROPPABLE          │
│                                             │
│  • policy (safety contract)                 │
└─────────────────────────────────────────────┘
```

This means:
- `sections:` in identity config is the **complete list** of persona-layer
  sections to load, in order
- `prompts/` files only affect sections already declared in `sections:`
- `policy` is never in `sections:` — it is always injected by the harness
  after persona sections
- An identity cannot accidentally (or intentionally) remove the safety
  contract
- Default content for **all** persona sections is on disk under
  `default/prompts/` for reference

## identity.md format

Single file with YAML frontmatter + markdown body. The body is the preamble.

**`sections:` is required.** It is the complete, declarative list of which
sections this identity loads and in what order.

### default/identity.md

```markdown
---
name: default
description: Built-in Gen Code persona — software engineering generalist
sections:
  - output
  - engineering
  - guidelines
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
  - output
  - engineering
  - guidelines
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

Note: `output` **must be declared in `sections:`** for `prompts/output.txt`
to take effect. If `output` were omitted from `sections:`, the file would be
ignored with a warning.

Other sections (`engineering`, `guidelines`, `environment`) are in
`sections:` but have no override files → fall back to `default/prompts/`.

Policy is always injected by the harness.

### my-custom/identity.md (user-defined, overrides output)

```markdown
---
name: my-custom
description: My custom role — concise output + engineering standards
sections:
  - output
  - engineering
  - guidelines
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

## Initialization

On `gen init` or first run, built-in identity content is extracted to
`~/.gen/identities/`. On every startup, integrity is verified:

```go
func Initialize(cwd string) {
    // Migrate flat .md files to directory structure (one-time)
    migrateFlatIdentityFiles()

    // Extract built-in identity files to disk (if missing)
    ensureIdentityDir("default", builtin.DefaultConfig, builtin.DefaultPrompts)
    ensureIdentityDir("readonly", builtin.ReadonlyConfig, builtin.ReadonlyPrompts)

    // Verify integrity of default prompts (every startup)
    verifyDefaultPrompts()
}

// ensureIdentityDir writes identity.md and prompts/ directory.
// All identities: existing files are NOT overwritten (respecting user modifications).
func ensureIdentityDir(name string, config []byte, prompts map[string][]byte) {
    dir := filepath.Join(identitiesDir, name)
    os.MkdirAll(filepath.Join(dir, "prompts"), 0755)

    // identity.md — write only if doesn't exist
    configPath := filepath.Join(dir, "identity.md")
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        os.WriteFile(configPath, config, 0644)
    }

    // prompts/ — write only if doesn't exist
    for filename, content := range prompts {
        p := filepath.Join(dir, "prompts", filename)
        if _, err := os.Stat(p); os.IsNotExist(err) {
            os.WriteFile(p, content, 0644)
        }
    }
}

// verifyDefaultPrompts checks integrity of default/prompts/ on every startup.
// If any file is missing or modified, it is restored from the canonical version.
func verifyDefaultPrompts() {
    for _, filename := range builtin.DefaultPromptFiles {
        diskPath := filepath.Join(defaultIdentityDir, "prompts", filename)
        canonical := builtin.ReadDefaultPrompt(filename)

        onDisk, err := os.ReadFile(diskPath)
        if os.IsNotExist(err) {
            log.Info("restoring missing default prompt: %s", filename)
            os.WriteFile(diskPath, canonical, 0644)
            continue
        }
        if err != nil {
            log.Warn("cannot read default prompt, restoring: %s (%v)", filename, err)
            os.WriteFile(diskPath, canonical, 0644)
            continue
        }
        if !bytes.Equal(onDisk, canonical) {
            log.Warn("default prompt was modified, restoring: %s", filename)
            os.WriteFile(diskPath, canonical, 0644)
        }
    }
}
```

Key behaviors:
- **`default/` and `readonly/` directories are auto-created on first init**
  — written only if they don't exist
- **Default prompts are verified on every startup** — modified files are
  overwritten with canonical versions from the binary
- **`readonly/` prompt files are never overwritten** once they exist
- **User-created identity directories are unaffected**
- **No silent upgrades** — if a prompt changes across versions, it is
  overwritten (the canonical version in the binary is always the source
  of truth)

## Where prompt content comes from

### Built-in prompts: compiled into binary, extracted to disk

Built-in identity prompt content is **embedded in the binary** (via
`//go:embed`) and written out to `~/.gen/identities/` on init.

```
Source (embedded in binary):               User directory (after init):
internal/identity/builtin/                 ~/.gen/identities/
├── default/                              ├── default/
│   ├── identity.md                       │   ├── identity.md    ← extracted on init
│   └── prompts/                          │   └── prompts/
│       ├── output.txt                    │       ├── output.txt ← extracted on init
│       ├── engineering.txt               │       ├── engineering.txt
│       ├── guidelines.txt                │       ├── guidelines.txt
│       └── environment.txt               │       └── environment.txt
└── readonly/                             ├── readonly/
    └── identity.md                       │   ├── identity.md    ← extracted on init
                                          │   └── prompts/
                                          │       └── (empty or user-created)
                                          └── code-reviewer/     ← user-created
                                              ├── identity.md
                                              └── prompts/
                                                  └── output.txt
```

### Resolution flow

```
┌─────────────────────────────────────────────────────────────────┐
│ For each section name in identity.Sections (in order):          │
│                                                                 │
│   <identity>/prompts/<section>.txt exists?                      │
│     ├── YES → use it (OVERRIDE)                                 │
│     └── NO  → fall back to default/prompts/<section>.txt        │
│                 ├── exists → use it (FALLBACK)                  │
│                 └── doesn't → skip this section (not injected)  │
│                                                                 │
│ Then, always append: policy (harness-injected)                  │
└─────────────────────────────────────────────────────────────────┘
```

```
Example: code-reviewer, sections: [output, engineering, guidelines, environment]

  output:
    code-reviewer/prompts/output.txt     → EXISTS → use it (CUSTOM)
  engineering:
    code-reviewer/prompts/engineering.txt → not found
    default/prompts/engineering.txt       → EXISTS → use it (FALLBACK)
  guidelines:
    code-reviewer/prompts/guidelines.txt  → not found
    default/prompts/guidelines.txt        → EXISTS → use it (FALLBACK)
  environment:
    code-reviewer/prompts/environment.txt → not found
    default/prompts/environment.txt       → EXISTS → use it (FALLBACK)
  + policy (harness)

Example: readonly, sections: [environment]

  environment:
    readonly/prompts/environment.txt      → not found
    default/prompts/environment.txt       → EXISTS → use it (FALLBACK)
  + policy (harness)
  (output, engineering, guidelines not in sections: → not loaded)
```

## Final system prompt assembly

```
default identity → sections: [output, engineering, guidelines, environment]
  + harness-injected: policy

  You are Gen Code, ...                                    ← preamble (from identity.md body)
  <output>                                                 ← default/prompts/output.txt
  ...
  </output>
  <engineering>                                            ← default/prompts/engineering.txt
  ...
  </engineering>
  <guidelines name="tool-usage">...</guidelines>           ← default/prompts/guidelines.txt
  <guidelines name="system-reminders">...</guidelines>
  ...
  <environment>                                            ← default/prompts/environment.txt
  date: 2026-06-05  cwd: /project  platform: darwin/arm64
  </environment>
  <policy>                                                 ← HARNESS: always injected
  ...
  </policy>

readonly identity → sections: [environment]
  + harness-injected: policy

  You are an AI assistant. ...                             ← preamble
  <environment>                                            ← fallback to default/prompts/environment.txt
  date: 2026-06-05  cwd: /project  platform: darwin/arm64
  </environment>
  <policy>                                                 ← HARNESS: always injected
  ...
  </policy>

code-reviewer identity → sections: [output, engineering, guidelines, environment]
  + harness-injected: policy

  You are a code review specialist. ...                    ← preamble
  <output>                                                 ← code-reviewer/prompts/output.txt (override)
  Be thorough. Cite file paths and line numbers.
  </output>
  <engineering>                                            ← fallback to default/prompts/engineering.txt
  ...
  </engineering>
  <guidelines>                                             ← fallback to default/prompts/guidelines.txt
  ...
  </guidelines>
  <environment>                                            ← fallback to default/prompts/environment.txt
  ...
  </environment>
  <policy>                                                 ← HARNESS: always injected
  ...
  </policy>
```

## User experience

### Creating a custom identity

```bash
# 0. Look at default prompts for reference
ls ~/.gen/identities/default/prompts/
# output.txt  engineering.txt  guidelines.txt  environment.txt
cat ~/.gen/identities/default/prompts/output.txt
# Shows the default output prompt — use as reference

# 1. Create directory
mkdir -p ~/.gen/identities/concise

# 2. Write identity.md (sections: is the complete list)
cat > ~/.gen/identities/concise/identity.md << 'EOF'
---
name: concise
description: Concise mode
sections:
  - output
  - engineering
  - guidelines
  - environment
---

You are a concise, direct coding assistant. Get to the point.
EOF

# 3. (Optional) Override a section's prompt
#    Must also be declared in sections: above
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

### Anti-pattern: override file without declaring section

```bash
# BAD: output.txt exists but output not in sections:
cat > ~/.gen/identities/bad-identity/identity.md << 'EOF'
---
name: bad-identity
description: This won't work as expected
sections:
  - engineering
  - environment
---

I am a bad example.
EOF

cat > ~/.gen/identities/bad-identity/prompts/output.txt << 'EOF'
<output>
This will NEVER be loaded. section "output" is not in sections:
</output>
EOF

# On startup:
# WARN: identity bad-identity: prompts/output.txt ignored — section "output" not declared in sections:
```

### Modifying default prompts (NOT allowed — will be reverted)

```bash
# This will be reverted on next startup:
vim ~/.gen/identities/default/prompts/output.txt

# On next startup:
# WARN: default prompt was modified, restoring: output.txt
#
# To customize output, create your own identity instead:
# mkdir -p ~/.gen/identities/my-style/prompts
# cp ~/.gen/identities/default/prompts/output.txt \
#    ~/.gen/identities/my-style/prompts/output.txt
# vim ~/.gen/identities/my-style/prompts/output.txt
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
  - guidelines
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
    Name        string     // Directory name, also the identity name
    Description string     // One-liner
    Preamble    string     // Identity declaration (body of identity.md)
    Sections    []string   // Complete list of persona-layer sections to load, in order
    Tools       ToolPolicy // Tool restrictions (merged with permission layer)
    Dir         string     // Path to this identity's directory
    SkillsDir   string     // Path to bundled skills/ (empty if none)
}
```

## Extensibility

### Adding a new built-in identity

1. Create a directory under `internal/identity/builtin/` with `identity.md`
2. Optionally add `prompts/` files
3. Add one line in `Initialize()`: `ensureIdentityDir("new-name", ...)`
4. Done. On next `gen init`, it's written to disk

### Adding a new persona-layer section type

1. Add a `.txt` file under the built-in default prompts
2. Add one line to the section→slot mapping table
3. Existing identities that declare the new section in their `sections:` will
   automatically load it (via fallback to `default/prompts/`)
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
| `prompts/*.txt` (embedded) | Default prompts extracted to `default/prompts/` on disk on init |
| `.gen/identities/` (project) | **Kept** — project wins on name collision |
| `applyDefaults()` | Hardcoded scope branches replaced by iterating over `Identity.Sections` |
| `core.Scope` | Keep Main/Subagent concept, but persona-layer composition is identity-driven |
| Policy | **Harness-enforced** — always injected, never droppable, never on disk |
| `/identity` | Scans both `.gen/identities/` and `~/.gen/identities/` |
| `settings.json` | `"identity": "readonly"` points to the directory name |
| Skill loader | Active identity's `skills/` added as a search root |
| Tool permissions | Merged into existing permission layer, not a parallel system |

## Default prompts content

Files under `default/prompts/` match the current `prompts/*.txt` content.
They are extracted from the binary on init and integrity-verified on every
startup:

| File | Corresponding Slot | Description |
|---|---|---|
| `output.txt` | SlotIdentity | Tone, updates, behavior |
| `engineering.txt` | SlotIdentity | Restraint, code conventions, error handling |
| `guidelines.txt` | SlotGuidelines | Tool usage, system reminders, task workflow, when to ask |
| `environment.txt` | SlotEnvironment | Environment template (with `{{.Date}}` etc. variables) |

`policy.txt` is **not** on disk — it is injected directly by the harness
and cannot be viewed, modified, or removed by the user.

## Decided questions

1. **Project-level identity directories?** → **Keep both.** `.gen/identities/`
   (project) and `~/.gen/identities/` (user). Project wins on name collision.
   `identity/registry.go` already resolves both today.

2. **Can defaults be modified by users?** → **No.** `default/prompts/` files
   are integrity-verified on every startup. Modified files are overwritten
   with canonical versions from the binary. Users who want customization
   should create their own identity directory.

3. **What happens if a user deletes `default/prompts/`?** → **Restored on
   next startup.** Missing files are re-extracted from the binary. Deleting
   the `default/` directory entirely does not break the system — it will
   be recreated on next init.

4. **Should upgrades update default prompts?** → **Yes, automatically.**
   Default prompts are verified on every startup against the canonical
   version in the binary. When a new version ships updated prompts, they
   overwrite the disk copies on next startup. Users who customized prompts
   in their own identity directories are unaffected.

5. **Can an identity remove the safety policy?** → **No.** Policy is not
   extracted to disk, not listed in `sections:`, and always injected by
   the harness. No identity can remove or modify it.

6. **How are existing flat `.md` identities handled?** → **One-time migration.**
   On first startup after upgrade, `~/.gen/identities/<name>.md` is moved to
   `~/.gen/identities/<name>/identity.md`. Non-breaking.

7. **Why extract defaults to disk instead of keeping them embedded-only?**
   → Users need a reference to know what they can customize. When creating a
   custom identity, the files under `default/prompts/` show exactly what
   sections exist, what format they use, and what the current defaults are.
   Without this, users would have to guess.

8. **`sections:` vs `prompts/` — which one controls what gets loaded?**
   → **`sections:` is the single source of truth.** It is the complete,
   declarative list of persona-layer sections to load, in order. `prompts/`
   files only provide override content for sections already declared in
   `sections:`. A file in `prompts/` for an undeclared section is ignored
   with a warning.

## References

- [identity.go](../../internal/identity/identity.go) — Current Identity struct
- [catalog.go](../../internal/core/system/catalog.go) — Current system prompt assembly
- [section.go](../../internal/core/section.go) — Section and Slot types
- [prompts/](../../internal/core/system/prompts/) — Currently embedded prompt files (will migrate to built-in default identity directory extracted on init)
