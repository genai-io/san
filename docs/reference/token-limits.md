# Token Limits

Token limits help track context window usage and prevent exceeding model limits. San supports both provider-supplied limits (from API) and user-configured limits.

## Storage

Token limits are stored in `~/.san/providers.json`:

```json
{
  "models": {
    "google:default": {
      "cachedAt": "2025-01-30T10:00:00Z",
      "models": [
        {
          "id": "gemini-2.0-flash",
          "name": "Gemini 2.0 Flash",
          "inputTokenLimit": 1048576,
          "outputTokenLimit": 8192
        }
      ]
    },
    "anthropic:vertex": {
      "cachedAt": "2025-01-30T10:00:00Z",
      "noExpire": true,
      "models": [
        {"id": "claude-opus-4-5@20251101", "name": "Claude Opus 4.5"}
      ]
    }
  },
  "tokenLimits": {
    "claude-opus-4-5@20251101": {
      "inputTokenLimit": 200000,
      "outputTokenLimit": 64000
    }
  }
}
```

## Data Sources

| Source | Location | Description |
|--------|----------|-------------|
| Model Cache | `models → {provider:auth} → models[]` | From provider's `ListModels()` API (e.g., Gemini) |
| Token Limits | `tokenLimits → {modelID}` | Manual override or auto-fetched values |

## Commands

| Command | Action |
|---------|--------|
| `/tokenlimit` | Show limits if available in model cache; otherwise auto-fetch |
| `/tokenlimit <input> <output>` | Set custom limits (e.g., `/tokenlimit 200000 64000`) |

## Fetch Logic (`/tokenlimit` command)

```
/tokenlimit
    │
    ▼
┌─────────────────────────────────┐
│ 1. Check Model Cache            │
│    (getModelTokenLimits)        │
└─────────────────────────────────┘
    │
    ├─── Has limits ───► Display (check tokenLimits for override)
    │                         │
    │                         ├─ Has override → Show with "(custom override)"
    │                         └─ No override → Show model cache values
    │
    └─── No limits ────► Auto-Fetch Agent
                              │
                              ▼
                    ┌─────────────────────────────────┐
                    │ Token Limit Agent               │
                    │ - Isolated context              │
                    │ - Max 5 turns                   │
                    │ - Tools: WebSearch, WebFetch    │
                    │ - Saves to tokenLimits          │
                    └─────────────────────────────────┘
```

### Auto-Fetch Agent

When model cache has no limits, an isolated agent is spawned:

- **System Prompt**: Specialized for finding token limits
- **Tools**: WebSearch, WebFetch only
- **Max Turns**: 5
- **Output Format**: `FOUND: <input> <output>` or `NOT_FOUND`
- **Isolation**: Does NOT pollute main conversation loop

```go
// The agent runs in complete isolation
systemPrompt := "You are a helpful assistant that finds token limits..."
messages := []provider.Message{
    {Role: "user", Content: "Find the token limits for model: ..."},
}
// Separate from main m.messages
```

## Display Logic (80% Usage Indicator)

When context usage reaches 80%+ of input limit:

```
⚡ 180K/200K (90%)
```

### Read Priority (`getEffectiveInputLimit`)

```go
func (m *model) getEffectiveInputLimit() int {
    // Priority 1: Custom override (tokenLimits)
    if inputLimit, _, ok := m.store.GetTokenLimit(modelID); ok {
        return inputLimit
    }
    // Priority 2: Model cache
    return getModelTokenLimits(m)
}
```

**Priority Order:**
1. `tokenLimits` (manual or auto-fetched) — **highest priority**
2. `Model Cache` (from provider API)
3. Return 0 (no limit known)

## Provider Differences

| Provider | ListModels API | Token Limits Source |
|----------|----------------|---------------------|
| Google (Gemini) | ✅ Yes | Model cache (API returns limits) |
| Anthropic | ❌ No | Auto-fetch or manual (`tokenLimits`) |
| OpenAI | ✅ Yes | Model cache (if API returns limits) |

### Anthropic Special Case

Anthropic providers use `noExpire: true` in model cache because they don't have a ListModels API. Token limits must come from:
1. Auto-fetch via `/tokenlimit`
2. Manual setting via `/tokenlimit <input> <output>`

## Implementation Files

| File | Purpose |
|------|---------|
| `internal/app/input/on_token_limits.go` | `/tokenlimit` slash command handler, auto-fetch agent |
| `internal/llm/store.go` | `SetTokenLimit`, `GetTokenLimit`, model cache |
| `internal/app/kit/token.go` | token usage rendering and 80% threshold indicator |
| `internal/app/conv/update.go` | `handleTokenLimitResult()` async result handling |

## Key Functions

### commands.go

```go
// Entry point for /tokenlimit command
handleTokenLimitCommand(ctx, m, args)

// Check model cache, start auto-fetch if needed
showOrFetchTokenLimits(ctx, m, modelID)

// Async agent that searches for token limits
autoFetchTokenLimits(ctx, m)

// Get limits from model cache only
getModelTokenLimits(m) (inputLimit, outputLimit int)

// Get effective limit for display (tokenLimits → model cache → 0)
getEffectiveInputLimit() int
```

### store.go

```go
// Save custom token limits
SetTokenLimit(modelID string, input, output int) error

// Get custom token limits
GetTokenLimit(modelID string) (input, output int, ok bool)

// Get cached models from provider
GetCachedModels(provider, authMethod) ([]ModelInfo, bool)
```

## UI Components

### Spinner During Fetch

When auto-fetching, a spinner is displayed in the chat area:

```
⠼ Fetching token limits...
```

The spinner is handled by:
- `fetchingTokenLimits` bool in model struct
- `handleSpinnerTick()` updates spinner when fetching
- `View()` renders spinner above input area

### Result Display

```
Token Limits for claude-opus-4-5@20251101:

  Input:  200K tokens
  Output: 64K tokens

(custom override)

Current usage: 150K tokens (75.0%)
```

## See Also

- [`packages/subagent.md`](../packages/2-feature/subagent.md) — Auto-fetch agent for token limits
- [`packages/mcp.md`](../packages/2-feature/mcp.md) — MCP tool responses contribute to token usage
