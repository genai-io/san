# cmd

**Layer:** cmd (rank 0, top of the stack)

The `cmd/san` entrypoint: flag parsing, dependency wiring, and the blank
imports that trigger each provider package's `init()` registration. It composes
the layers below it and owns nothing else — all behaviour lives in `1-app` and
the `2-feature` packages it wires together.

See [`../../reference/cli-startup.md`](../../reference/cli-startup.md) for the
startup sequence and [`../../reference/package-map.md`](../../reference/package-map.md)
for the authoritative layer assignment.
