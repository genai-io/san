# Proposal: Identity Rework — Directory-based Prompt Configuration

## Summary

Redesign Identity as a **directory structure** under `~/.gen/identities/`.
Each identity is a directory containing:
- `identity.yaml` — role configuration (name, preamble, section list, tool permissions)
- `prompts/` — the prompt files used by this role

**Core mechanism: file-based fallback.** When an identity is missing a prompt
file, the same-named file from the `default` identity is used automatically.
Custom identities only need to contain the **differences** from default.

The first new built-in identity: **`readonly`**, with minimal prompts and
read-only tools.

## Directory structure

```
~/.gen/identities/
│
├── default/                        ← default identity (built-in, written on init)
│   ├── identity.yaml               ← role configuration
│   └── prompts/                    ← full set of default prompts
│       ├── output.txt
│       ├── engineering.txt
│       ├── policy.txt
│       ├── guidelines.txt
│       └── environment.txt
│
├── readonly/                       ← read-only identity (built-in, written on init)
│   ├── identity.yaml
│   └── prompts/                    ← mostly empty, everything falls back to default
│       └── environment.txt         ← optional; can also be omitted (fallback)
│
└── my-custom/                      ← user-defined identity
    ├── identity.yaml
    └── prompts/
        └── output.txt              ← only overrides output; rest fall back to default
```

### Compared to current structure

```
Current:                                New:
~/.gen/identities/                      ~/.gen/identities/
├── README.md                           ├── default/
└── ml-engineer.md    ← single .md file │   ├── identity.yaml
                                        │   └── prompts/
                                        │       ├── output.txt
                                        │       ├── engineering.txt
                                        │       ├── policy.txt
                                        │       ├── guidelines.txt
                                        │       └── environment.txt
                                        ├── readonly/
                                        │   ├── identity.yaml
                                        │   └── prompts/
                                        │       └── (empty, all fallback)
                                        └── my-custom/
                                            ├── identity.yaml
                                            └── prompts/
                                                └── output.txt
```

## Fallback mechanism

This is the core of the design. When loading a section prompt for an identity,
the system searches in this order:

```
Loading the "output" section for identity "readonly":

  1. ~/.gen/identities/readonly/prompts/output.txt   ← identity's own
  2. ~/.gen/identities/default/prompts/output.txt    ← fallback to default

If neither exists → section is empty, not injected
```

```go
func resolvePrompt(identityDir string, sectionName string) string {
    // 1. Look for the identity's own prompt file
    p := filepath.Join(identityDir, "prompts", sectionName+".txt")
    if content, ok := readFile(p); ok {
        return content
    }
    // 2. Fall back to default identity's same-named file
    p = filepath.Join(defaultIdentityDir, "prompts", sectionName+".txt")
    if content, ok := readFile(p); ok {
        return content
    }
    // 3. Neither exists → don't inject this section
    return ""
}
```

### What this means

**default identity**: all prompt files exist → uses entirely its own content
(equivalent to current behavior)

**readonly identity**: `prompts/` directory is basically empty → all sections
would fall back to default's prompt files. But `identity.yaml` only declares
the `environment` section, so only environment is loaded.

**Custom identity**: only needs prompt files that **differ from default**.
For example, to just change the output style:

```
my-custom/
├── identity.yaml         ← declares sections: [output, engineering, policy, guidelines, environment]
└── prompts/
    └── output.txt        ← its own output; other four fall back to default
```

## identity.yaml format

### default/identity.yaml

```yaml
name: default
description: Built-in Gen Code persona — software engineering generalist

preamble: |
  You are Gen Code, an interactive AI assistant for software
  engineering tasks running in a terminal.

# Declares which sections to load.
# Each section's prompt content is loaded from prompts/<name>.txt.
# If the file doesn't exist, falls back to default identity's same-named file.
sections:
  - output
  - engineering
  - policy
  - guidelines
  - environment

# Tool permissions
tools:
  # empty = all available
```

### readonly/identity.yaml

```yaml
name: readonly
description: Read-only assistant — search, analyze, answer questions

preamble: |
  You are an AI assistant. You can read files, search code, and answer
  questions. You cannot modify files or execute commands.

sections:
  - environment

tools:
  allow:
    - Read
    - Grep
    - Glob
    - WebSearch
    - WebFetch
    - Task
```

### code-reviewer/identity.yaml

```yaml
name: code-reviewer
description: Code review specialist — read-only, focused on logic and style

preamble: |
  You are a code review specialist. Carefully read code to find bugs,
  security vulnerabilities, performance issues, and style inconsistencies.

sections:
  - engineering
  - policy
  - environment

tools:
  allow:
    - Read
    - Grep
    - Glob
```

### my-custom/identity.yaml (user-defined, overrides output)

```yaml
name: my-custom
description: My custom role — concise output + engineering standards

preamble: |
  You are a concise coding assistant. One sentence per response, max.

sections:
  - output
  - engineering
  - policy
  - guidelines
  - environment

tools: {}
```

Together with `my-custom/prompts/output.txt`:

```
<output>
Always concise. Use bullet points. Never apologize.
</output>
```

The other four sections (engineering, policy, guidelines, environment)
automatically fall back to files in `default/prompts/`.

## Initialization

On `gen init` or first run, built-in identities are written to
`~/.gen/identities/`:

```go
func Initialize(cwd string) {
    ensureIdentityDir("default", builtin.DefaultIdentityConfig, builtin.DefaultPrompts)
    ensureIdentityDir("readonly", builtin.ReadonlyIdentityConfig, builtin.ReadonlyPrompts)
    // More built-in identities can be added in the future
}

// ensureIdentityDir writes identity.yaml and prompts/ directory.
// default identity: prompt files can be overwritten on upgrade (user changes will be lost).
// Other identities: existing files are NOT overwritten (respecting user modifications).
func ensureIdentityDir(name string, config []byte, prompts map[string][]byte) {
    dir := filepath.Join(identitiesDir, name)
    os.MkdirAll(filepath.Join(dir, "prompts"), 0755)

    // identity.yaml — write only if doesn't exist (all identities)
    configPath := filepath.Join(dir, "identity.yaml")
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        os.WriteFile(configPath, config, 0644)
    }

    // prompts/ — default always updates; others write only if doesn't exist
    for filename, content := range prompts {
        p := filepath.Join(dir, "prompts", filename)
        if name == "default" {
            // Default identity: upgrades can overwrite prompt files
            os.WriteFile(p, content, 0644)
        } else if _, err := os.Stat(p); os.IsNotExist(err) {
            os.WriteFile(p, content, 0644)
        }
    }
}
```

Key behaviors:
- **`default` and `readonly` directories are auto-created on first init**
- **`default` prompt files are overwritten on upgrade** — new Gen Code
  versions will update the default prompts. User modifications to default
  prompts will be lost on upgrade
- **`default`'s identity.yaml and `readonly` directory are never overwritten**
  — protecting user's configuration changes
- **If the `default` directory is deleted, it will be restored on init**
- **User-created identity directories are unaffected by init**

## Where prompt content comes from

### Built-in prompts: compiled into binary, extracted to disk

Built-in identity prompt content is **embedded in the binary** (via
`//go:embed`) and written out to `~/.gen/identities/` on init.

```
Source (embedded in binary):               User directory (after init):
internal/identity/builtin/                 ~/.gen/identities/
├── default/                              ├── default/
│   ├── identity.yaml                     │   ├── identity.yaml    ← extracted
│   └── prompts/                          │   └── prompts/
│       ├── output.txt                    │       ├── output.txt   ← extracted
│       ├── engineering.txt               │       ├── engineering.txt
│       ├── policy.txt                    │       ├── policy.txt
│       ├── guidelines.txt                │       ├── guidelines.txt
│       └── environment.txt               │       └── environment.txt
└── readonly/                             ├── readonly/
    └── identity.yaml                     │   └── identity.yaml    ← extracted
                                          └── my-custom/           ← user-created
                                              ├── identity.yaml
                                              └── prompts/
                                                  └── output.txt
```

### Resolution priority

```
Loading the "output" section for identity "my-custom":

  my-custom/prompts/output.txt  exists? → use it
                        doesn't? ↓
  default/prompts/output.txt    exists? → use it
                        doesn't? ↓
                        don't inject this section

Loading sections for identity "readonly":
  sections: [environment]

  → Load the environment section:
    readonly/prompts/environment.txt  exists? → use it
                             doesn't? ↓
    default/prompts/environment.txt   exists? → use it
                             doesn't? ↓
                             don't inject

  → output/engineering/policy/guidelines not declared → not loaded, not injected
```

## Final system prompt assembly

```
default identity → sections: [output, engineering, policy, guidelines, environment]

  You are Gen Code, ...                                    ← preamble
  <output>                                                 ← default/prompts/output.txt
  ...
  </output>
  <engineering>                                            ← default/prompts/engineering.txt
  ...
  </engineering>
  <policy>                                                 ← default/prompts/policy.txt
  ...
  </policy>
  <guidelines name="tool-usage">...</guidelines>           ← default/prompts/guidelines.txt
  <guidelines name="system-reminders">...</guidelines>
  ...
  <environment>                                            ← default/prompts/environment.txt
  date: 2026-06-04  cwd: /project  platform: darwin/arm64
  </environment>

readonly identity → sections: [environment]

  You are an AI assistant. ...                             ← preamble
  <environment>                                            ← fallback to default/prompts/environment.txt
  date: 2026-06-04  cwd: /project  platform: darwin/arm64
  </environment>

my-custom identity → sections: [output, engineering, policy, guidelines, environment]

  You are a concise coding assistant. ...                  ← preamble
  <output>                                                 ← my-custom/prompts/output.txt (its own)
  Always concise. Use bullet points. Never apologize.
  </output>
  <engineering>                                            ← fallback to default
  <policy>                                                 ← fallback to default
  <guidelines>                                             ← fallback to default
  <environment>                                            ← fallback to default
```

## User experience

### Creating a custom identity

```bash
# 1. Create directory
mkdir -p ~/.gen/identities/concise/prompts

# 2. Write identity.yaml
cat > ~/.gen/identities/concise/identity.yaml << 'EOF'
name: concise
description: Concise mode
preamble: "You are a concise, direct coding assistant. Get to the point."
sections:
  - engineering
  - policy
  - environment
tools: {}
EOF

# 3. (Optional) Override a section's prompt
cat > ~/.gen/identities/concise/prompts/output.txt << 'EOF'
<output>
Answer directly. No small talk. No apologies.
</output>
EOF

# 4. Switch to the identity
gen> /identity concise
✓ Identity switched: concise
```

### Modifying default prompts (affects all identities)

```bash
# Edit the default prompt files directly
vim ~/.gen/identities/default/prompts/output.txt

# All identities without their own output.txt (including readonly, my-custom, etc.)
# will use the modified default prompt
```

### Deleting a custom identity

```bash
rm -rf ~/.gen/identities/my-custom
# Completely removed; default and other identities unaffected
```

## Identity data structure

```go
type Identity struct {
    Name        string   // Directory name, also the identity name
    Description string   // One-liner
    Preamble    string   // Identity declaration
    Sections    []string // Section names to load, in order
    AllowTools  []string // Tool allowlist (empty = all)
    DenyTools   []string // Tool denylist
    Dir         string   // Path to this identity's directory
}
```

## Extensibility

### Adding a new built-in identity

1. Create a directory under `internal/identity/builtin/` with `identity.yaml`
2. Optionally add `prompts/` files
3. Add one line in `Initialize()`: `ensureIdentityDir("new-name", ...)`
4. Done. On next `gen init`, it's written to disk

### Adding a new section type

1. Add a `.txt` file under `default/prompts/` (e.g. `testing.txt`)
2. Add one line to the section→slot mapping table
3. Existing identities that declare the new section in their list will
   automatically load it
4. Identities that don't declare it are unaffected

### Community sharing

```bash
# Export an identity (directory tarball)
tar -czf my-architect.tar.gz -C ~/.gen/identities architect/

# Import
tar -xzf my-architect.tar.gz -C ~/.gen/identities/
gen> /identity architect
```

## Relationship with existing system

| Existing concept | Change |
|---|---|
| `~/.gen/identities/*.md` | Single .md files replaced by directory structure |
| `prompts/identity.txt` etc. | Content moves to `default/prompts/` files, written on init |
| `applyDefaults()` | Hardcoded scope branches replaced by iterating over `Identity.Sections` |
| `core.Scope` | Keep Main/Subagent concept, but section composition is identity-driven |
| `/identity` | Auto-scans subdirectories under `~/.gen/identities/` |
| `settings.json` | `"identity": "readonly"` points to the directory name |

## Default prompts content

Files under `default/prompts/` match the current `prompts/*.txt` content:

| File | Corresponding Slot | Description |
|---|---|---|
| `output.txt` | SlotIdentity | Tone, updates, behavior |
| `engineering.txt` | SlotIdentity | Restraint, code conventions, error handling |
| `policy.txt` | SlotPolicy | Safety contract |
| `guidelines.txt` | SlotGuidelines | Tool usage, system reminders, task workflow, when to ask |
| `environment.txt` | SlotEnvironment | Environment template (with `{{.Date}}` etc. variables) |

## Decided questions

1. **Project-level identity directories?** → **Not supported.** Only use
   `~/.gen/identities/`, no `.gen/identities/` project-level directory.
   Keep it simple.

2. **Can the default identity be deleted?** → **Not allowed.** If the user
   deletes `~/.gen/identities/default/`, it will be restored on next init.
   Other identities depend on default as fallback; deleting it would break
   the system.

3. **Should upgrades update default prompts?** → **Yes.** New Gen Code
   versions will overwrite default prompt files on init. User modifications
   to default prompts will be lost on upgrade. Users who want to persist
   customizations should put them in their own identity directory.

## References

- [identity.go](../../internal/identity/identity.go) — Current Identity struct
- [catalog.go](../../internal/core/system/catalog.go) — Current system prompt assembly
- [section.go](../../internal/core/section.go) — Section and Slot types
- [prompts/](../../internal/core/system/prompts/) — Currently embedded prompt files (will migrate to built-in default identity directory)
