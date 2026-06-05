# Development

## Common Commands

```bash
make build
make test
make lint
make format
```

## Sandbox-Friendly Test Command

Some environments block writes to the default Go build cache. Use a writable
cache when needed:

```bash
GOCACHE=/private/tmp/san-go-build-cache go test ./...
```

## Formatting

`make format` runs `gofmt` and `goimports`. Install `goimports` with:

```bash
make install-format-tools
```
