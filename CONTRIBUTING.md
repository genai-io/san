# Contributing to San

Thanks for your interest in contributing! This guide will help you get started.

## Quick Start

```bash
git clone https://github.com/genai-io/san.git
cd san
go build -o san ./cmd/san
./san
```

## Development

### Prerequisites

- Go 1.25+
- An LLM API key (Anthropic, OpenAI, or Google)

### Project Structure

```
cmd/               # Binary entrypoints (cmd/san)
docs/              # Documentation (architecture.md + packages/ + concepts/ + reference/ + guides/ + operations/)
internal/          # All Go code; see docs/reference/package-map.md for the full table
tools/             # Developer tooling (layercheck, …)
tests/integration/ # Cross-package behavioral tests
notes/             # Work-in-progress plans (not durable docs)
```

See [`docs/architecture.md`](docs/architecture.md) for primitives and
the runtime model. See [`docs/reference/package-map.md`](docs/reference/package-map.md)
and [`docs/reference/dependency-rules.md`](docs/reference/dependency-rules.md)
for the full package list, layer assignment, and import rules. New
package contributions also need a [`docs/packages/<name>.md`](docs/packages/)
following [`docs/packages/TEMPLATE.md`](docs/packages/TEMPLATE.md).

### Run Tests

```bash
GOCACHE=/private/tmp/san-go-build-cache go test ./...
```

Transcript/session focused suites:

```bash
GOCACHE=/private/tmp/san-go-build-cache go test \
  ./internal/session/... ./tests/integration/session/... ./tests/integration/cli/...
```

Transcript storage layout, recording rules, and the event model are
documented in [`docs/packages/session.md`](docs/packages/session.md).

### Debug Mode

```bash
SAN_DEBUG=1 ./san
# Logs written to ~/.san/debug.log
```

## How to Contribute

### Report Bugs

Use the [Bug report](https://github.com/genai-io/san/issues/new?template=bug_report.yml)
template. For security vulnerabilities, follow [SECURITY.md](SECURITY.md) instead —
do not open a public issue.

### Suggest Features

Use the [Feature request](https://github.com/genai-io/san/issues/new?template=feature_request.yml)
template. For open-ended ideas, start a
[discussion](https://github.com/genai-io/san/discussions) first.

### Submit Code

1. Fork the repo
2. Create a branch: `git checkout -b feature/your-feature`
3. Make changes and test
4. Commit with sign-off: `git commit -s -m "feat: add feature"`
5. Push and open a PR

### Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add new feature
fix: resolve bug
docs: update documentation
refactor: restructure code
test: add tests
chore: maintenance tasks
```

## Areas for Contribution

| Area | Description |
|------|-------------|
| **Providers** | Add new LLM providers (Ollama, Mistral, etc.) |
| **Tools** | Create new built-in tools |
| **MCP** | Improve MCP server support |
| **TUI** | Enhance terminal UI/UX |
| **Docs** | Improve documentation |
| **Tests** | Increase test coverage |

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md).
All participants are expected to uphold these standards.

## Security

Found a security vulnerability? Please do **not** open a public issue.
See our [security policy](SECURITY.md) for private reporting instructions.

## Questions?

Start a [discussion](https://github.com/genai-io/san/discussions). We're happy to help!
