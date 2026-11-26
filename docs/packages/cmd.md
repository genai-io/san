# cmd

**Layer:** cmd (rank 0, top of the stack)

The `cmd/san` entrypoint parses flags, wires dependencies, and selects the host:
`tui.Run` for interactive sessions, `cli.Print` for print mode, or
`acp.Run` for `san acp`. Host packages own execution after dispatch.

See [`../reference/cli-startup.md`](../reference/cli-startup.md) for the
startup sequence and [`../reference/package-map.md`](../reference/package-map.md)
for the authoritative layer assignment.
