# ADR-0003: Shared Work Queue for Multi-Persona Coordination

## Status

Proposed — 2026-06-16.

## Context

ADR-0002 defines an autonomous development management model where multiple
personas (Leader, Dev, QE, Release) run as independent San instances and
coordinate through a shared work queue.

The queue must:
- Allow multiple independent processes to safely claim and update tasks
  without a central server or database
- Be human-readable and git-versionable for crash recovery
- Support a task lifecycle state machine: pending → claimed → done → verified
- Work with shell scripts (`run.sh` polling loop) without requiring a Go binary
  in the team repo

## Decision

Implement a **file-based JSONL work queue** served by a new `san queue`
subcommand in the San CLI.

### Relationship to Persona System

`san queue` and `san --persona <name> -p` are independent, composable features:

- **`san queue`** manages the shared work queue: add, claim, complete, verify,
  release, fail, list. It knows nothing about personas or LLM agents.
- **`san --persona <name> -p`** loads a persona from `~/.san/personas/`,
  injects its system prompt (identity + behavior + rules), and runs a headless
  agent. It knows nothing about the queue.

The `run.sh` script in san-team composes them:

```
run.sh → san queue claim → san --persona  -p ".."→ san queue complete/release
```

This separation means `san queue` is independently useful beyond san-team —
any multi-agent orchestration scenario, CI pipeline, or task scheduler can
use it.

### Storage format

```
<team-dir>/state/queue.jsonl
```

Each line is a compact JSON object. Status changes append new lines (same ID,
updated fields). Readers deduplicate by ID, keeping the latest line. This
makes the queue trivially recoverable from Git history (`git checkout --
state/queue.jsonl`).

### Work item schema

```json
{
  "id": "a1b2c3d4e5f6a7b8",
  "role": "dev",
  "title": "Implement JWT token generation",
  "description": "Create internal/core/jwt/ package...",
  "status": "claimed",
  "assignedTo": "dev",
  "pr": "",
  "result": "",
  "createdAt": "2026-06-16T10:00:00Z",
  "updatedAt": "2026-06-16T10:30:00Z",
  "claimedAt": "2026-06-16T10:30:00Z",
  "retryCount": 0,
  "maxRetries": 3
}
```

### State machine

```
pending ──claim()──→ claimed ──complete()──→ done ──verify()──→ verified
   ↑                     │                       │
   └──release()─── ← (timeout)          ← reject() ──┘
                         │
                         └──fail()──→ failed
```

- `claim`: atomic read→find pending→append claimed operation. Uses `flock` on
  `<dir>/queue.lock` to prevent two processes claiming the same item.
- `release`: reverts claimed→pending, bumps retryCount.
- `fail`: when retryCount >= maxRetries, mark as permanently failed.
- Timeout release: claimed items older than 10 minutes auto-revert to pending.

### CLI surface

```
san queue list     --dir <state-dir> [--role <r>] [--status <s>]
san queue claim    --dir <state-dir> --role <r> --persona <n>
san queue add      --dir <state-dir> --role <r> --title "..." --description "..."
san queue complete --dir <state-dir> --id <id> --persona <n> [--pr <url>] [--result "..."]
san queue verify   --dir <state-dir> --id <id> --persona <n> [--result "..."]
san queue release  --dir <state-dir> --id <id> --persona <n> [--reason "..."]
san queue fail     --dir <state-dir> --id <id> --persona <n> [--reason "..."]
```

Note: there is no `san queue prompt` command. Prompt composition is done by
`run.sh` (extracting `title` and `description` from the claimed task JSON).
Persona system prompt injection is handled by `san --persona <name> -p`.

### Package structure

```
internal/queue/        ← feature layer
  item.go              WorkItem, ItemStatus, state transitions, ID generation
  queue.go             JSONLStore with flock-based atomic append
  claim.go             Claimer: claim, release, complete, verify, fail + stale release
cmd/san/queue.go       Cobra subcommand wiring
```

## Complete Example: Authentication Feature Lifecycle

This shows how queue items flow through the state machine during a real
feature implementation. The same example lives at
[`san-team/state/EXAMPLE.md`](../../../san-team/state/EXAMPLE.md).

### 1. Leader creates tasks (all start as pending)

Leader breaks down "Build user authentication" and runs:

```bash
san queue add --dir state/ --role dev \
  --title "Define User model and UserStore interface" \
  --description "Create User struct (ID, Username, PasswordHash, CreatedAt) in internal/core/user.go. Define UserStore interface with FindByUsername, Create, FindByID methods."

san queue add --dir state/ --role dev \
  --title "Implement UserStore with SQLite" \
  --description "Implement UserStore interface using SQLite in internal/feature/userstore/. Parameterized queries, bcrypt password hashing."

san queue add --dir state/ --role dev \
  --title "Implement JWT token generation and verification" \
  --description "Create internal/core/jwt/ package with GenerateToken and ValidateToken. HS256, 24h expiry."

san queue add --dir state/ --role dev \
  --title "Implement POST /auth/login handler" \
  --description "Create login API handler at internal/app/authhandler/. Accept {username, password}, return {token}."

san queue add --dir state/ --role dev \
  --title "Implement auth middleware" \
  --description "Create middleware that extracts Bearer token, validates, injects user info into context."

san queue add --dir state/ --role dev \
  --title "Add rate limiting to login endpoint" \
  --description "Limit POST /auth/login to 5 attempts per minute per IP."

san queue add --dir state/ --role qe \
  --title "Verify auth module" \
  --description "Check out all auth PRs. Run make test, make lint, make layercheck. Add integration tests under test-integration/auth/."

san queue add --dir state/ --role release \
  --title "Ship v1.3.0 with user authentication" \
  --description "Generate CHANGELOG, bump version to 1.3.0, tag v1.3.0, generate release notes."
```

Queue after Leader's work:

```
ID        Role     Title                                   Status    Assigned
a1b2c3d4  dev      Define User model and interface         pending   -
b2c3d4e5  dev      Implement UserStore with SQLite         pending   -
c3d4e5f6  dev      Implement JWT token generation          pending   -
d4e5f6a7  dev      Implement POST /auth/login              pending   -
e5f6a7b8  dev      Implement auth middleware               pending   -
f6a7b8c9  dev      Add rate limiting to login              pending   -
a7b8c9d0  qe       Verify auth module                      pending   -
b8c9d0e1  release  Ship v1.3.0                             pending   -
```

### 2. Dev claims and implements

Dev's `run.sh` runs: `san queue claim --dir state/ --role dev --persona dev`

Claim is atomic (flock-protected). Only one process can claim a given item.

```
Claim Task 1 → status: claimed, assignedTo: dev
  san --persona dev -p ".." → implements → PR #123
  san queue complete --id a1b2c3d4 --pr "https://github.com/genai-io/san/pull/123"
  → status: done

Claim Task 2 → status: claimed, assignedTo: dev
  san --persona dev -p ".." → implements → PR #124
  san queue complete --id b2c3d4e5 --pr "https://github.com/genai-io/san/pull/124"
  → status: done

... (Tasks 3-6 similarly)
```

### 3. QE verifies

QE's `run.sh` polls for `role: qe` tasks. When all dev tasks are done:

```
$ san queue claim --dir state/ --role qe --persona qe
→ Claims Task a7b8c9d0 (Verify auth module), status: claimed

$ san --persona qe -p "Verify auth module..."
→ Checks out PRs #123-#126 → runs tests → adds integration tests → passes

$ san queue verify --id a7b8c9d0 --persona qe --result "All tests pass. Integration tests added."
→ status: verified
```

### 4. Release ships

Release's `run.sh` polls for `role: release` tasks:

```
$ san queue claim --dir state/ --role release --persona release
→ Claims Task b8c9d0e1 (Ship v1.3.0), status: claimed

$ san --persona release -p "Ship v1.3.0..."
→ Generates CHANGELOG → bumps version → git tag v1.3.0

$ san queue complete --id b8c9d0e1 --persona release
→ status: done
```

### 5. Failure and retry example

```
Task f6a7b8c9: "Fix nil pointer in auth middleware"
  Attempt 1: claimed → agent fails → release → pending (retryCount: 1)
  Attempt 2: claimed → agent fails → release → pending (retryCount: 2)
  Attempt 3: claimed → agent fails → release → pending (retryCount: 3)
  retryCount >= maxRetries → san queue fail → status: failed

Leader detects failed task and notifies admin.
```

### Full queue state at completion

```bash
$ san queue list --dir state/
```

```
ID        Role     Title                                   Status    Assigned  PR
a1b2c3d4  dev      Define User model and interface         done      dev       #123
b2c3d4e5  dev      Implement UserStore with SQLite         done      dev       #124
c3d4e5f6  dev      Implement JWT token generation          done      dev       #125
d4e5f6a7  dev      Implement POST /auth/login              done      dev       #126
e5f6a7b8  dev      Implement auth middleware               done      dev       #127
f6a7b8c9  dev      Add rate limiting to login              done      dev       #128
a7b8c9d0  qe       Verify auth module                      verified  qe        -
b8c9d0e1  release  Ship v1.3.0                             done      release   -

Total: 8 | pending: 0 | claimed: 0 | done: 7 | verified: 1 | failed: 0
```

### Interaction with run.sh

The complete `run.sh` loop showing how queue and agent compose:

```bash
#!/bin/bash
set -euo pipefail
TEAM_DIR="$(cd "$(dirname "$0")" && pwd)"
PERSONA="${1:?usage: $0 <leader|dev|qe|release>}"
CWD="${2:-$(pwd)}"
INTERVAL="${3:-30}"

echo "[san-team:$PERSONA] polling every ${INTERVAL}s, cwd=$CWD"

while true; do
  TASK=$(san queue claim --dir "$TEAM_DIR/state" --role "$PERSONA" --persona "$PERSONA" 2>/dev/null || true)
  if [ -n "$TASK" ]; then
    ID=$(echo "$TASK" | jq -r '.id')
    TITLE=$(echo "$TASK" | jq -r '.title')
    DESC=$(echo "$TASK" | jq -r '.description')
    echo "[san-team:$PERSONA] claimed $ID: $TITLE"

    PROMPT="Task: $TITLE

    $DESC

    After completing, summarize what you did and any PR links."

    if san --persona "$PERSONA" -p "$PROMPT"; then
      san queue complete --dir "$TEAM_DIR/state" --id "$ID" --persona "$PERSONA"
      echo "[san-team:$PERSONA] completed $ID"
    else
      san queue release --dir "$TEAM_DIR/state" --id "$ID" --persona "$PERSONA"
      echo "[san-team:$PERSONA] released $ID (will retry)"
    fi
  fi
  sleep "$INTERVAL"
done
```

Key design insight: `run.sh` is the only component that touches both the
queue and the agent. The queue doesn't know about personas. The agent
doesn't know about the queue. The shell script is the composition layer.

## Consequences

- **Positive**: No database dependency. The queue is a single JSONL file that
  can be inspected with `cat`, searched with `grep`, and recovered from Git.
- **Positive**: `san queue` is independently useful beyond san-team. Any
  multi-agent orchestration scenario can use it.
- **Positive**: The CLI surface is scriptable — the polling loop in `san-team`
  is a 12-line shell script.
- **Positive**: Clean separation of concerns. `san queue` handles atomic queue
  operations. `san --persona <name> -p` handles persona-aware agent execution.
  `run.sh` composes them.
- **Negative**: JSONL append-only grows unboundedly. Periodic compaction (keep
  only the latest line per ID) should be added as a `san queue compact` command
  later.
- **Negative**: File-based locking means all queue operations must be on the
  same filesystem. Cross-machine coordination requires a shared filesystem (NFS)
  or the shell fallback over SSH.

## References

- [ADR-0002](0002-autonomous-dev-management.md) — autonomous development management team
- [`san-team/DESIGN.md`](../../../san-team/DESIGN.md) — prompt-first san-team design
- [`san-team/state/EXAMPLE.md`](../../../san-team/state/EXAMPLE.md) — queue examples
