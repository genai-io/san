# Skill System

Skills are reusable capabilities that extend GenCode's functionality. They allow users to define custom workflows, scripts, and instructions that the LLM can invoke programmatically. The skill system aligns with the [Agent Skills specification](https://agentskills.io) and provides Claude Code compatibility.

## Overview

Key principles:
- **Progressive loading**: Only load content when needed to conserve context window
- **Script execution**: Scripts in skill directories can be executed directly
- **Simplicity first**: Context window is a shared resource; only add what Claude doesn't know

## Directory Structure

### Skill Directory Layout

```
skill-name/
├── SKILL.md            # Required - skill definition
│   ├── YAML frontmatter
│   │   ├── name: skill-name
│   │   ├── description: Short description and trigger conditions
│   │   ├── namespace: optional-namespace
│   │   └── allowed-tools: [Bash, Read, Write]
│   └── Markdown instructions
└── Optional resources
    ├── scripts/        # Executable scripts (Python/Bash)
    ├── references/     # Reference documents (loaded on demand)
    └── assets/         # Output resources (templates, images)
```

### Search Paths (Priority Order)

Skills are loaded from multiple locations, with higher priority paths overriding lower ones:

| Priority | Path | Scope | Description |
|----------|------|-------|-------------|
| 1 (lowest) | `~/.claude/skills/` | ClaudeUser | Claude Code user skills |
| 2 | `~/.claude/plugins/*/skills/` | UserPlugin | Claude plugin skills |
| 3 | `~/.gen/plugins/*/skills/` | UserPlugin | GenCode plugin skills |
| 4 | `~/.gen/skills/` | User | GenCode user skills |
| 5 | `.claude/skills/` | ClaudeProject | Project Claude skills |
| 6 | `.claude/plugins/*/skills/` | ProjectPlugin | Project plugin skills |
| 7 | `.gen/plugins/*/skills/` | ProjectPlugin | GenCode project plugins |
| 8 (highest) | `.gen/skills/` | Project | Project-specific skills |

## Architecture

### Loading Flow

```
┌─────────────┐
│   main.go   │
│  Application│
└──────┬──────┘
       │
       ▼
┌─────────────────┐
│ skill.Initialize│
│   (cwd)         │
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Loader.LoadAll()                           │
│                                                                 │
│  For each search path:                                          │
│    1. Scan for SKILL.md files                                   │
│    2. Parse YAML frontmatter (name, description, allowed-tools) │
│    3. Scan scripts/, references/, assets/ directories           │
│    4. Register skill in registry                                │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Registry (Global)                             │
│                                                                  │
│  skills map[string]*Skill     ← Indexed by FullName             │
│  userStore *Store             ← ~/.gen/skills.json persistence  │
│  projectStore *Store          ← .gen/skills.json persistence    │
│                                                                  │
│  Methods:                                                        │
│  • Get(name) → *Skill           Find by exact name              │
│  • FindByPartialName(name)      Fuzzy find (commit → git:commit)│
│  • GetEnabled() → []*Skill      Get enabled skills              │
│  • GetActive() → []*Skill       Get active skills (model-aware) │
│  • SetState(name, state)        Set state and persist           │
│  • GetSkillsSection()           Generate system prompt section   │
└─────────────────────────────────────────────────────────────────┘
```

### Skill State Management

Skills have three states that control visibility and invocation:

```
○ disable ────────► ◐ enable ────────► ● active ────────► (cycle)
  (Disabled)         (Enabled)          (Active)

  • Invisible        • Visible          • Visible
  • Cannot invoke    • User can invoke  • User can invoke
                     • /skill-name      • LLM can invoke
                                        • In system prompt
```

State is persisted in:
- `~/.gen/skills.json` for user-level state
- `.gen/skills.json` for project-level state (takes priority)

### Skill Data Structure

```go
type Skill struct {
    // Frontmatter (YAML)
    Name         string   // Skill name
    Namespace    string   // Namespace (git, jira, etc.)
    Description  string   // Description (for system prompt)
    AllowedTools []string // Permitted tools
    ArgumentHint string   // Argument hint text

    // Runtime
    FilePath     string     // SKILL.md path
    SkillDir     string     // Skill directory path
    Scope        SkillScope // Scope level
    Instructions string     // Markdown content
    State        SkillState // State (disable/enable/active)

    // Resources (Agent Skills Spec)
    Scripts    []string // Files in scripts/
    References []string // Files in references/
    Assets     []string // Files in assets/
}
```

## Skill Tool

The Skill tool allows the LLM to invoke skills programmatically.

### Tool Schema

```json
{
  "name": "Skill",
  "description": "Execute a skill within the main conversation",
  "parameters": {
    "type": "object",
    "properties": {
      "skill": {
        "type": "string",
        "description": "The skill name (e.g., 'commit', 'git:pr', 'pdf')"
      },
      "args": {
        "type": "string",
        "description": "Optional arguments for the skill"
      }
    },
    "required": ["skill"]
  }
}
```

### Invocation Flow

```
┌──────────────────────────────────────────────────────────────────┐
│                      Method A: User Slash Command                 │
│                                                                   │
│   User input: /commit -m "Fix bug"                                │
│                │                                                  │
│                ▼                                                  │
│   ExecuteCommand() → ParseCommand() → IsSkillCommand()            │
│                │                                                  │
│                ▼                                                  │
│   executeSkillCommand()                                           │
│     • GetSkillInvocationPrompt() ← Full instructions as XML      │
│     • Inject into next conversation turn                          │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│                      Method B: LLM Tool Call                      │
│                                                                   │
│   LLM calls: Skill(skill="commit", args="-m 'Fix bug'")          │
│                │                                                  │
│                ▼                                                  │
│   SkillTool.Execute()                                             │
│     1. Registry.Get(skillName)         Find skill                │
│     2. FindByPartialName(skillName)    Fuzzy match               │
│     3. Check IsEnabled()               Must be enabled           │
│     4. GetInstructions()               Load full instructions    │
│                │                                                  │
│                ▼                                                  │
│   Build <skill-invocation> response:                              │
│                                                                   │
│   <skill-invocation name="git:commit">                            │
│   User arguments: -m 'Fix bug'                                    │
│                                                                   │
│   Available scripts (use Bash to execute):                        │
│     - /path/to/skill/scripts/commit.sh                            │
│                                                                   │
│   Reference files (use Read when needed):                         │
│     - /path/to/skill/references/CONVENTIONS.md                    │
│                                                                   │
│   [SKILL.md content...]                                           │
│   </skill-invocation>                                             │
│                │                                                  │
│                ▼                                                  │
│   Return to LLM → LLM executes instructions                       │
└──────────────────────────────────────────────────────────────────┘
```

### System Prompt Integration

Active skills are included in the system prompt as the `<skills>` directory
(slot 5). The body lists name + one-line description; the `<skills>`
envelope is added by the catalog. See
[System Prompt → Skills](system-prompt.md#skills--agent-injection) for
the slot model and how `Skill.Registry.GetSkillsSection()` plugs in.

```xml
<skills>
Use the Skill tool to invoke these capabilities:

- git:commit: Create git commits with DCO sign-off [2 scripts]
- pdf: Process PDF files [3 scripts, 2 refs]
- my-skill: Do something useful

Invoke with: Skill(skill="name", args="optional args")
</skills>
```

## Progressive Loading

The skill system uses three-level progressive loading to conserve context:

| Level | When Loaded | Content |
|-------|-------------|---------|
| 1 | Always (in `<skills>` slot) | Name + description (~100 words) |
| 2 | On skill invocation | Full SKILL.md instructions (<5k words) |
| 3 | On demand | Resource files (scripts, references, assets) |

### Level 1: Always in slot 5

`Registry.GetSkillsSection()` returns plain body text (no XML wrapper);
the system catalog adds `<skills>...</skills>`. Only **active** skills
appear (state machine in §Skill State Management).

### Level 2: Skill Invocation

When the LLM calls `Skill(name, args)`, the SkillTool returns the full
content via `Registry.GetSkillInvocationPrompt(name)`. When the user types
`/<skill-name>`, the body is also injected as `Skill.PendingInstructions`
and prepended to the next user message — see
[System Prompt → Invocation](system-prompt.md#invocation-slot-6).

### Level 3: Resource Files

LLM uses Read/Bash tools to access resources as needed:

```markdown
# Skill Instructions

For API documentation, see [references/API.md](references/API.md)
Run `scripts/process.py` to execute the workflow.
```

## UI/UX

### Skill Invocation Display

When a skill is invoked, the display shows:

```
⚡Skill(git:commit)
  ⎿  Loaded: git:commit [2 scripts, 1 ref]
```

- Skill name displayed prominently
- Resource counts shown (scripts, refs)
- Raw content hidden from user (LLM sees it)

### Execution Spinner

While loading:

```
⚡Skill
  ⠹ Loading skill...
```

### Error Display

When skill not found or disabled:

```
⚡Skill(unknown-skill)
  ✗  Skill not found: unknown-skill
```

## Implementation Reference

### Key Files

| File | Description |
|------|-------------|
| `internal/skill/types.go` | Skill struct and state definitions |
| `internal/skill/loader.go` | SKILL.md parsing and resource scanning |
| `internal/skill/registry.go` | Global skill registry |
| `internal/skill/store.go` | State persistence |
| `internal/tool/skill.go` | Skill tool implementation |
| `internal/tool/ui/skill.go` | Skill result rendering |
| `internal/tui/render.go` | TUI rendering integration |
| `internal/tui/commands.go` | Slash command handling |

### Creating a Skill

1. Create directory: `~/.gen/skills/my-skill/`
2. Create `SKILL.md`:

```yaml
---
name: my-skill
description: Short description of what this skill does
allowed-tools:
  - Bash
  - Read
argument-hint: "[--verbose]"
---

# My Skill

Instructions for the LLM...
```

3. Optionally add scripts:

```bash
mkdir scripts
cat > scripts/run.sh << 'EOF'
#!/bin/bash
echo "Hello from my-skill!"
EOF
chmod +x scripts/run.sh
```

4. Enable the skill: Use `/skill` command in GenCode

## Compatibility

### Claude Code Compatibility

- Supports `~/.claude/skills/` directory
- Supports `.claude/skills/` directory
- Supports plugin skills from `installed_plugins.json`
- SKILL.md format is fully compatible

### Agent Skills Specification

- Supports standard directory structure
- Supports scripts/references/assets directories
- Implements progressive loading
- .skill package format (optional, not implemented)

## See Also

- [Plugin System](plugin-system.md) — Skills can be bundled and distributed as plugins
- [System Prompt](system-prompt.md) — Slot/Section model and progressive loading strategy
- [Subagent System](subagent.md) — Agent-based execution (isolated loop vs skill injection)

## References

- [Agent Skills Specification](https://agentskills.io/specification)
- [Anthropic Skills Repository](https://github.com/anthropics/skills)
- [Claude Code Skills Documentation](https://code.claude.com/docs/en/skills)
- [How to Create Custom Skills](https://support.claude.com/en/articles/12512198-how-to-create-custom-skills)
