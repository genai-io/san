# Small Footprint & Portability

San ships as **one self-contained binary**. There is no Node.js runtime,
no Python interpreter, no package manager, and no `node_modules` tree to
install or keep in sync. You copy a single file, mark it executable, and run
it — which is exactly what makes it portable enough for tiny, locked-down, and
far-from-the-datacenter machines.

This page explains *why small matters* and *where that lets you run*. For the
raw measured numbers and methodology, see
[benchmark.md](benchmark.md).

## How small is it?

| Dimension | San | Why it matters |
|---|---|---|
| Download (compressed `.tar.gz`) | **~12 MB** | Fits on metered, slow, or air-gapped links |
| On-disk binary | **~40 MB** | One file, no surrounding directory tree |
| File count | **1** | Nothing to unpack, link, or `npm install` |
| Runtime dependencies | **0** | No Node.js, no Python, no shared libraries to provision |
| Startup memory | **~32 MB** | Leaves headroom on 256–512 MB devices |
| Cold start | **~0.01s** | Cheap to launch in short-lived shells, CI steps, and hooks |

A typical Node.js-based assistant, by contrast, needs its own ~60 MB package
*plus* a ~110 MB Node.js runtime on disk before it can do anything — and pays a
heavier baseline in RAM and startup time. San carries its entire world
inside the single executable.

## Why a single static binary

San is compiled with Go. On Linux the released binaries are **statically
linked** — running `file` on one reports `statically linked ... stripped`,
meaning there are no external `.so` dependencies to resolve at load time. The
result:

- **No interpreter, no VM.** Native machine code starts instantly; there is no
  JIT warm-up, no garbage-collected runtime to spin up first.
- **No dependency hell.** There is no lockfile to reconcile, no transitive
  package CVE surface to patch, no global vs. project version skew. The binary
  you tested is the binary you ship.
- **Trivial install / uninstall.** Install is "put the file on `PATH`."
  Uninstall is "delete the file." Nothing is scattered across the filesystem.
- **Reproducible everywhere.** The same artifact behaves the same on a laptop,
  a CI runner, and an edge box, because it brings its own runtime with it.

## Where you can run it

Because the binary is small and dependency-free, it fits places a heavier
toolchain can't follow:

- **Minimal containers** — copy the binary into a `scratch`, `distroless`, or
  Alpine image. The image layer is tiny and there is no base runtime to carry,
  which keeps pulls fast and the attack surface small.
- **Edge nodes and IoT gateways** — drop one binary onto a constrained edge
  device; no need to provision a language runtime alongside it.
- **CI runners** — download and cache ~12 MB instead of installing Node.js and
  resolving an `npm` tree on every job. The ~0.01s cold start keeps pipeline
  steps cheap.
- **Air-gapped / restricted hosts** — bastions, jump hosts, and offline
  machines where you can copy a file in but cannot run a package-manager
  install. One `scp` and you're done.
- **Ephemeral and serverless environments** — short-lived VMs and functions
  where a fast cold start and a small artifact matter more than anything.
- **Raspberry Pi and other SBCs** — small enough that the `linux/arm64` build
  even runs on a Pi 3/4/5 (64-bit OS) and similar ARM single-board computers:
  ~12 MB to fetch, ~32 MB resident.

## Try it on a Raspberry Pi (or any arm64 Linux)

```bash
# On a 64-bit Raspberry Pi OS / arm64 Linux box:
curl -fsSL https://raw.githubusercontent.com/genai-io/san/main/install.sh | bash
san --version
```

The installer fetches the matching `linux/arm64` archive (~12 MB), unpacks the
single `san` binary, and puts it on your `PATH`. Nothing else is installed.

## Building for other targets

The release matrix covers `darwin/{amd64,arm64}` and `linux/{amd64,arm64}` (see
the `release` target in the [`Makefile`](../../Makefile)). Because it is plain
Go, you can cross-compile for almost any other target Go supports — older 32-bit
ARM Pis, Windows, FreeBSD, RISC-V, and more — by setting two environment
variables, with no C toolchain required:

```bash
# Example: 32-bit ARM (older Pi / Pi Zero), fully static, stripped
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
  go build -ldflags "-s -w" -o san ./cmd/san
```

## Summary

Small is not just a vanity metric. A ~12 MB, zero-dependency, single-file binary
is what lets San run on a Raspberry Pi, slip into a `scratch` container,
land on an air-gapped host, and cold-start inside a CI step — all without first
dragging along a Node.js or Python runtime. The footprint *is* the portability.
