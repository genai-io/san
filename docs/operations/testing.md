# Testing

## Local Commands

```bash
make test
go test ./...
```

When Go cache writes are blocked:

```bash
GOCACHE=/private/tmp/san-go-build-cache go test ./...
```

## Placement

- Unit tests should live with the package they verify.
- Cross-package behavior can use integration tests under `tests/integration`.
- Add focused tests for package-boundary changes and service wiring changes.
