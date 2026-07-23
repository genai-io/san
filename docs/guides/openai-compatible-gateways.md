# Using an OpenAI/Anthropic-Compatible Gateway

## Overview

Many teams reach their models through an aggregating **gateway** — a token
reseller, an internal proxy, or a self-hosted router (OneAPI / NewAPI and
similar). A gateway speaks a familiar wire protocol (OpenAI or Anthropic),
issues you its own API key, and forwards requests to whatever upstream models
it fronts (Claude, GPT, Gemini, DeepSeek, GLM, Qwen, …).

San does not need a dedicated provider for this. Every built-in provider is
built on the official OpenAI or Anthropic Go SDK, so you point San at the
gateway by **overriding the base URL** and supplying the gateway's key. This
guide shows the two supported paths and when to pick each.

## TL;DR

```bash
# OpenAI-protocol gateway (recommended — one endpoint, all its models)
export OPENAI_API_KEY="<gateway-key>"
export OPENAI_BASE_URL="https://your-gateway.example/v1"

# Anthropic-protocol gateway
export ANTHROPIC_API_KEY="<gateway-key>"
export ANTHROPIC_BASE_URL="https://your-gateway.example"
```

Then launch San and pick a model with `/model`. Confirm the exact base-URL
path (with or without a trailing `/v1`) against your gateway's own docs.

## How San resolves the base URL

San's OpenAI and Anthropic providers construct the SDK client **without**
hard-coding an endpoint
([`internal/llm/openai/apikey.go`](../../internal/llm/openai/apikey.go),
[`internal/llm/anthropic/apikey.go`](../../internal/llm/anthropic/apikey.go)),
so the official SDKs fall back to their standard environment variables —
`OPENAI_BASE_URL` and `ANTHROPIC_BASE_URL`. Setting those redirects every
request through the gateway while San's streaming, tool-call, and usage
handling stay unchanged.

Values can come from a shell export, an `.env` file, or San's secret store —
[`secret.Resolve`](../../internal/secret/store.go) reads the process
environment first, then the stored value.

## Path A — OpenAI protocol (recommended)

Most gateways expose an OpenAI-compatible `/v1` surface, and this path has one
decisive advantage: San's OpenAI provider discovers models **dynamically** by
calling the gateway's `/v1/models` endpoint
([`internal/llm/openai/client.go`](../../internal/llm/openai/client.go),
`ListModels`) instead of a fixed built-in catalog. Whatever models the gateway
has enabled for your key show up in `/model` — including non-OpenAI models such
as Claude or Gemini routed through the same endpoint.

```bash
export OPENAI_API_KEY="<gateway-key>"
export OPENAI_BASE_URL="https://your-gateway.example/v1"
```

**Caveat — token limits.** San infers context/output windows from the model ID
([`internal/llm/openai/catalog.go`](../../internal/llm/openai/catalog.go),
`openAILimits`), and that name matching only recognizes OpenAI's own `gpt-*` /
`o*` families. A Claude or Gemini model reached over the OpenAI protocol is
still fully usable, but its context window may fall back to a default and
display imprecisely. This affects only limit display and compaction thresholds,
not correctness.

## Path B — Anthropic protocol, or borrowing a provider's base-URL override

Use this when your gateway speaks the Anthropic protocol, or when you want a
built-in catalog's token limits instead of dynamic discovery.

**Anthropic protocol:**

```bash
export ANTHROPIC_API_KEY="<gateway-key>"
export ANTHROPIC_BASE_URL="https://your-gateway.example"
```

**Borrow a compatible provider slot.** Several providers already read a
`<PROVIDER>_BASE_URL` override, so you can route them through the gateway while
keeping their model catalog. The model IDs the gateway serves must match that
provider's expected IDs.

| Provider | Base-URL override |
|---|---|
| DeepSeek | `DEEPSEEK_BASE_URL` |
| Moonshot (Kimi) | `MOONSHOT_BASE_URL` |
| Alibaba (Qwen) | `DASHSCOPE_BASE_URL` |
| Z.ai (GLM) | `BIGMODEL_BASE_URL` |
| MiniMax | `MINIMAX_BASE_URL` / `MINIMAX_OPENAI_BASE_URL` |
| SenseNova | `SENSENOVA_BASE_URL` |
| MiMo | `MIMO_BASE_URL` |
| Volcengine (Ark) | `VOLCENGINE_BASE_URL` |
| Agnes-AI | `AGNESAI_BASE_URL` |
| Ollama | `OLLAMA_BASE_URL` |

Example:

```bash
export DEEPSEEK_API_KEY="<gateway-key>"
export DEEPSEEK_BASE_URL="https://your-gateway.example/v1"
```

## Choosing a path

| Want | Use |
|---|---|
| One endpoint that exposes every model the gateway fronts | Path A (OpenAI) |
| Anthropic-native features (prompt caching, thinking blocks) | Path B (Anthropic) |
| A built-in provider's exact token limits | Path B (borrow a slot) |

When in doubt, start with **Path A** — it is the least configuration and lists
the gateway's full model set automatically.

## Where to put the values

- **Shell / `.env`** — quickest for a single machine (see
  [`.env.example`](../../.env.example)).
- **`~/.san/settings.json`** `env` map — persists across sessions; see
  [`reference/configuration.md`](../reference/configuration.md).
- **Secret store** — for keys you would rather not keep in plaintext.

## Troubleshooting

- **`/model` is empty or errors on Path A** — the gateway's `/v1/models` path
  is wrong or your key has no models enabled. Verify the base URL and that the
  key lists models via a plain `curl "$OPENAI_BASE_URL/models"`.
- **404 / wrong path** — gateways differ on whether the base URL includes
  `/v1`. Match your gateway's documented example exactly.
- **A model's context window looks wrong** — expected on Path A for non-OpenAI
  models (see the caveat above); it does not affect request correctness.
