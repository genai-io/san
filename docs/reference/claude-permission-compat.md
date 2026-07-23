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

## Ways to Configure

1. **Edit settings files directly** — any of the locations listed above
2. **`/permissions` command** inside Claude Code — interactive rule management
3. **CLI flags** (session-only):
   ```bash
   claude --permission-mode acceptEdits
   claude --add-dir /tmp/data
   claude --disallowedTools "Bash(rm *)"
   ```

## San Subagent Permissions

San does not load `.claude/agents/*.md` definitions or their `allow_tools` /
`deny_tools` frontmatter. The Agent tool launches one implicit worker and accepts
only these model-facing modes:

| Agent mode | Behavior |
|------------|----------|
| `default` | Dynamically inherits the parent session's effective mode, including bypass. |
| `explore` | Authoritative read-only ceiling; mutations stay blocked. |
| `edit` | Reads and file edits are allowed; other calls that need approval are denied. |

Workers have no interactive approval channel, so an `ask` decision collapses to
deny. The model cannot explicitly request bypass mode. See
[`concepts/permission-model.md`](../concepts/permission-model.md).

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
- **Security boundary**: `.claude/` configuration (hooks, settings, etc.) in additional directories is **not loaded** — only `.claude/skills/` is discovered
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
