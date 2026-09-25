# Token Limits

A model publishes two figures, and San uses them as the vendor means them:

- **context window**: the most tokens one request may use, prompt and reply
  together (OpenAI: "includes input, output, and reasoning tokens").
- **max output**: the most one reply may produce. It is part of the window,
  not added to it.

So the prompt a request can carry depends on how much it keeps for the reply:
`prompt + max_tokens ≤ window`.

## How San uses them

| Figure | Rule | Where |
|---|---|---|
| `max_tokens` sent | `min(max output, 32k)`; 8192 when unknown | `llm.OutputCap` |
| Auto-compact point | `window − max_tokens` | `llm.PromptBudget` |
| Prompt size | the provider's count for the last call + an estimate of what followed | `core.promptMeasure` |

A reply cut off at `max_tokens` is continued, not lost, so holding the cap to
32k costs nothing but frees the rest of the window for the prompt. The status
bar reads against the same budget: 100% is where auto-compaction fires.

An unknown window stays unknown: no proactive compaction, the prompt-too-long
retry recovers, and the status bar shows `--`.

## Where the figures come from

In order, first hit wins (`llm.Store.EffectiveContextWindow`):

1. A hand-set override: `/context limit`, stored under `tokenLimits` in
   `~/.san/providers.json`.
2. This provider's cached model listing, which the model data under it fills
   in (models.dev, San's own corrections, then `~/.san/models.json`).
3. The largest window cached for the same model ID under any provider.

## Setting them by hand

```
/context                          # usage, window, and where it auto-compacts
/context limit 200000 64000       # override the current model's window and max output
/context limit reset              # drop the override
```

Use the vendor's figures as published. To correct a model for every session,
`~/.san/models.json` takes the models.dev shape, keyed by vendor:

```json
{ "openai": { "models": { "<model-id>": { "limit": { "context": 1050000, "output": 128000 } } } } }
```

A live listing that states a window outranks `models.json`; the override
outranks both.
