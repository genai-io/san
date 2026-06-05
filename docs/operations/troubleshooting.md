# Troubleshooting

## Go Cache Permission Errors

If `go test ./...` fails while writing the Go build or module cache, use a cache
inside a writable directory:

```bash
GOCACHE=/private/tmp/san-go-build-cache go test ./...
```

## Missing goimports

Install formatting tools with:

```bash
make install-format-tools
```
