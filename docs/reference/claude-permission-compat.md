# Claude Code Permissions in Settings

## Permission Modes

Switch modes mid-session with `Shift+Tab` or set a default in settings:

| Mode | Auto-approves | Use case |
|------|---------------|----------|
| **default** | Reads only | Sensitive work |
| **acceptEdits** | Reads + file edits + common fs commands (`mkdir`, `touch`, `mv`, `cp`, `rm`, `rmdir`) | Code iteration |
| **plan** | Reads only (analyze before editing) | Exploring codebases |
| **auto** | Everything (with safety classifier) | Long tasks, less prompting |
| **dontAsk** | Only pre-approved tools in `allow` rules | CI/scripting |
| **bypassPermissions** | Everything, no checks | Isolated containers only |

## Settings File Locations

Highest to lowest precedence:

| Scope | Location | Shared? |
|-------|----------|---------|
| Managed | Server/plist/registry | IT-deployed, immutable |
| Local project | `.claude/settings.local.json` | No (gitignored) |
| Shared project | `.claude/settings.json` | Yes (commit to git) |
| User | `~/.claude/settings.json` | Personal, all projects |

Permission arrays **merge** across scopes. A `deny` at any level cannot be overridden by `allow` at a lower level.

## Rule Format

Evaluation order: **Deny > Ask > Allow** (first match wins).

### Configuration Example

```json
{
  "permissions": {
    "defaultMode": "acceptEdits",
    "allow": [
      "Bash(npm run *)",
      "Bash(git commit *)",
      "Read(./src/**)",
      "WebFetch(domain:github.com)"
    ],
    "ask": [
      "Bash(git push *)"
    ],
    "deny": [
      "Bash(rm -rf *)",
      "Read(./.env)",
      "Read(./.env.*)",
      "Read(./secrets/**)"
    ]
  }
}
```

### Pattern Syntax

**Bash patterns** (support `*` wildcards at any position):

- `Bash(npm run *)` — matches any npm run command
- `Bash(git *)` — matches all git commands
- `Bash(* --version)` — matches any command ending with `--version`

Spacing matters: `Bash(ls *)` matches `ls -la` but NOT `lsof`. Use `Bash(ls*)` to match both.

**Read/Edit rules** (gitignore-style paths):

- `Read(./src/**)` — relative to current directory (recursive)
- `Edit(/docs/**)` — relative to project root (recursive)
- `Read(~/.ssh/id_rsa)` — home directory absolute path
- `Read(//Users/alice/secrets)` — filesystem absolute path (note double slash)

**Other tools**:

- `WebFetch(domain:example.com)` — domain-scoped fetch
- `MCP(mcp__puppeteer)` — MCP server tools
- `Agent(Explore)` — specific subagent type

## Ways to Configure

1. **Edit settings files directly** — any of the locations listed above
2. **`/permissions` command** inside Claude Code — interactive rule management
3. **CLI flags** (session-only):
   ```bash
   claude --permission-mode acceptEdits
   claude --add-dir /tmp/data
   claude --disallowedTools "Bash(rm *)"
   ```

## Subagent vs Main Loop Permissions

Subagents do **not** inherit the main session's permission rules. They use an independent, fixed permission mode:

| permissionMode | Behavior | Use case |
|----------------|----------|----------|
| (default) ReadOnly | Read files, grep, web fetch only. No Bash or file writes. | Explore and research agents |
| `"plan"` | Same as ReadOnly | Analysis and planning |
| `"acceptEdits"` | Reads and file edits allowed. Bash still requires approval. | Agents that need to write code |
| `"default"` | Permissive mode | Agents that need the full toolchain |

The main session's allow/deny rules from settings.json are **not propagated** to subagents. Even if the main session allows `Bash(npm:*)`, a subagent still cannot run Bash unless its permissionMode explicitly permits it.

### Agent Definition Tool Restrictions (YAML)

Custom agents are defined in `.claude/agents/*.md` with YAML frontmatter. Two fields control tool access:

#### allow_tools — Schema Filter

When specified, the LLM only sees these tools in its schema and cannot attempt to use others. When omitted, all tools are available.

```yaml
# Comma-separated string
allow_tools: Read, Bash

# Array form
allow_tools: [Read, Bash]

# Array with patterns
allow_tools:
  - Read
  - Bash(git diff*)       # Only git diff commands
  - Bash(go test*)        # Only go test commands
```

An `allow_tools` entry naming a tool San does not ship (for example `Glob`
or `Grep` from a Claude Code agent definition) is simply inert: no schema
matches it, so it grants nothing. If such an agent needs to search, list
`Bash` (optionally with patterns) — read-only commands (`rg`, `grep`,
`find`, `ls`, read-only `git`, …) auto-permit in every mode, and a
whitelisted agent only sees the tools its list names.

#### deny_tools — Denylist (Always Wins)

Always overrides `allow_tools` and permission mode. Denied tools are removed from the LLM schema entirely.

```yaml
deny_tools:
  - Agent
  - SendMessage
  - Bash(rm *)            # Block dangerous bash patterns
```

#### Evaluation Order

```
1. allow_tools filter  → not in list? blocked, LLM never sees it
2. deny_tools filter   → always blocks, regardless of allow_tools
3. mode rules          → explore/acceptEdits/default apply remaining checks
4. execute or ask user
```

#### Examples

**Read-only code reviewer:**

```yaml
---
name: code-reviewer
description: Reviews code for bugs and security issues
mode: explore
allow_tools:
  - Read
  - Bash(git diff*)
  - Bash(git log*)
  - Bash(git show*)
  - WebFetch
deny_tools:
  - Agent
  - Write
  - Edit
---

You are a code reviewer. Analyze the code for correctness, security, and style...
```

**Implementation agent (can edit, restricted bash):**

```yaml
---
name: implementer
description: Implements code changes
mode: acceptEdits
allow_tools:
  - Read
  - Write
  - Edit
  - Bash(go test*)
  - Bash(go build*)
  - Bash(make *)
deny_tools:
  - Agent
---

You are an implementation agent. Write clean, tested code...
```

**Full agent definition fields:**

```yaml
---
name: agent-name
description: "What this agent does"
model: opus                  # inherit | sonnet | opus | haiku
mode: explore                # explore | acceptEdits | default | bypassPermissions
allow_tools: [Read, Bash]    # Schema filter
deny_tools: [Agent]          # Always wins
max-steps: 100
color: blue                  # UI display color
when-to-use: "When to use this agent"
skills: []                   # Additional skills to load
mcp-servers: []              # MCP servers for this agent
---

System prompt content goes here in the markdown body...
```

## Bash Compound Command Injection Protection

Under a `Bash(git status)` rule, `git status; rm -rf /` is **not allowed**.

Claude Code uses a shell AST parser to split compound commands:

- Splits on `&&`, `||`, `;`, `|`, `&`, and newlines into independent subcommands
- **Each subcommand is matched independently** — `git status` matches allow, but `rm -rf /` does not, so the entire command is blocked
- The `*` wildcard in `Bash(git *)` does **not cross** separators — in `git log; evil`, `evil` must match a rule on its own
- "Always allow" approval on compound commands saves separate rules for each subcommand (up to 5), never a single broad rule

**Process wrapper stripping**: `timeout`, `time`, `nice`, `nohup`, `stdbuf` are stripped before matching. `Bash(npm test)` matches `timeout 30 npm test`. However, `watch`, `find -exec`, and `ionice` are not stripped and always require approval.

## additionalDirectories

```json
"additionalDirectories": ["../shared-docs/"]
```

Extends Claude Code's file access scope beyond the working directory:

- Files in these directories become **readable** (same treatment as cwd)
- File editing still follows the current permission mode
- **Security boundary**: `.claude/` configuration (hooks, settings, agents, etc.) in additional directories is **not loaded** — only `.claude/skills/` is discovered
- Can be set via CLI `--add-dir ../shared-docs/`, in-session `/add-dir`, or persisted in settings.json

## Ask Approval Behavior

When a rule is in the `ask` list, executing that tool presents a 4-option approval dialog:

| Option | Behavior | Persistence |
|--------|----------|-------------|
| **Yes** | Allow this single use only | Next identical call prompts again |
| **Yes, allow all during session** | Allow this tool class for the rest of the session | Cleared when session ends |
| **Always allow** | Permanently allow, written to settings.json | Persists across sessions |
| **No** | Deny this single use | Next identical call prompts again |

- "Always allow" on compound commands splits into separate rules per subcommand
- Permanent rules can be removed by editing settings.json or running `/reset-permissions`

## Key Behavioral Details

- **Protected paths**: `.git/`, `.claude/`, `.vscode/`, `.idea/`, `.husky/`, shell configs, MCP configs are **never** auto-approved except in `bypassPermissions`.
- **Read-only commands**: Built-in safe commands (`ls`, `cat`, `grep`, `find`, `git log`, etc.) run without prompts in all modes.
- **Symlinks**: Allow rules check both symlink and target; deny rules block if either matches.

## Other Considerations

### Managed Settings (Enterprise)

Administrators can enforce policies via managed settings:

- `disableBypassPermissionsMode: "disable"` — prevents users from using bypassPermissions mode
- `allowManagedPermissionRulesOnly: true` — prevents users from adding their own allow rules
- Register `PreToolUse` hooks for custom audit logic (e.g., logging all Bash commands)

### Permission Rule Pitfalls

- **Deny is absolute**: once denied at any scope level, lower-level allow rules cannot override it. If team `.claude/settings.json` denies a tool, personal `settings.local.json` allow has no effect.
- **`*` does not match empty**: `Bash(git *)` matches `git status` but does **not** match bare `git` (requires at least one argument after the space).
- **Path `**` vs `*`**: `Read(./src/*)` matches one level only; `Read(./src/**)` matches all subdirectories recursively.
- **MCP tool naming**: MCP tools use the `mcp__serverName__toolName` format. `deny` with `MCP(mcp__puppeteer__*)` blocks the entire server.

### dontAsk Mode and CI/CD

In `dontAsk` mode, only tools in the allow list are executed. Everything else is silently denied (no prompt). Suitable for non-interactive scenarios:

```bash
claude --permission-mode dontAsk \
  --allowedTools "Bash(npm test)" \
  --allowedTools "Bash(npm run build)" \
  -p "run tests and build"
```

## Full Configuration Example

```json
{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "permissions": {
    "defaultMode": "acceptEdits",
    "allow": [
      "Bash(npm run build)",
      "Bash(npm run test *)",
      "Bash(git commit *)",
      "Bash(git log:*)",
      "Bash(git checkout *)",
      "Read(./src/**)",
      "Read(./docs/**)",
      "WebFetch(domain:github.com)",
      "WebFetch(domain:docs.anthropic.com)"
    ],
    "deny": [
      "Bash(rm -rf *)",
      "Bash(git push *)",
      "Read(./.env)",
      "Read(./.env.*)",
      "Read(./secrets/**)"
    ],
    "additionalDirectories": [
      "../shared-docs/"
    ]
  },
  "env": {
    "NODE_ENV": "development"
  },
  "model": "claude-opus-4-6"
}
```
