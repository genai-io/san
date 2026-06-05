---
name: release
description: >-
  Create a new versioned release with changelog. Bumps version in code, updates CHANGELOG.md,
  commits, tags, and pushes using the repository release flow. GitHub Actions creates the release
  and uses only the current changelog section as release notes. Use when the user says "release",
  "cut a release", "bump version", "new release".
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Edit
argument-hint: "<version>"
---

# Release — Version Bump, Tag, and Changelog

Create a new release with a structured changelog and author attribution.

## Arguments

- `<version>` — The version to release (e.g. `1.19.1`, `v2.0.0`). The `v` prefix is optional in input but always used for the git tag.

If no version is given, detect the current version and suggest the next one (see Step 1).

## Workflow

### 1. Detect current version and suggest the next

Read the current version and identify changes since the last tag:

1. Read the code version from `cmd/san/main.go`:
   ```bash
   grep 'var version = ' cmd/san/main.go
   ```

2. Get the latest git tag:
   ```bash
   git describe --tags --abbrev=0
   ```
   Strip the `v` prefix for comparison.

3. **Compare.** If the code version and tag version differ, warn the user — the code may be out of sync with releases.

4. Get commits since the latest tag:
   ```bash
   git log --oneline <prev_tag>..HEAD
   ```

5. **Suggest the next semver bump** based on commit content:
   - **Major** (`X.0.0`): commits with `BREAKING CHANGE`, or that remove/rename public APIs
   - **Minor** (`x.Y.0`): `feat:` commits, new functionality, new features
   - **Patch** (`x.y.Z`): `fix:` commits, bug fixes, docs, chores, refactors

   Default to **patch** if the commits don't clearly indicate otherwise.

6. **Ask the user.** Use AskUserQuestion, showing the current code version and the suggested next version:
   - Option 1: the suggested version (e.g. `v1.19.1` — patch)
   - Option 2: one step larger bump (e.g. `v1.20.0` — minor)
   - The "Other" option lets the user enter a custom version.

   Example: if current is `1.19.0` and commits are all `fix:` and `chore:`, offer:
   ```
   Current version: 1.19.0 → suggest v1.19.1 (patch)
   Options: [v1.19.1, v1.20.0]
   ```

### 2. Update the changelog

Add a new `CHANGELOG.md` section for the target version. Keep older sections in place. The format must match:

```markdown
## [vX.Y.Z] - YYYY-MM-DD

### Added
- ...

### Changed
- ...

### Fixed
- ...
```

Use the commit log from Step 1 as source material. Group entries under `Added`, `Changed`, or `Fixed` based on conventional commit prefixes.

Write only the current version section in `CHANGELOG.md`. Do not pass the entire file as manual release notes later; the GitHub Actions workflow extracts the current version section automatically.

### 3. Bump the version in source code

Update the version string in `cmd/san/main.go`:

```go
var version = "<new_version>"
```

### 4. Commit, tag, and push

Stage the version bump and changelog update, then commit with sign-off:

```bash
git add cmd/san/main.go CHANGELOG.md
git commit -s -m "chore: bump version to <new_version>"
```

Push the release using the Makefile helper:

```bash
make release-push VERSION=v<new_version>
```

This target validates that the working tree is clean, the tag does not already exist, and `CHANGELOG.md` contains the matching section before it pushes `main` and the tag.

The tag push triggers the GitHub Actions release workflow (`.github/workflows/release.yml`) which builds binaries, creates the GitHub release, and uses only the current changelog section as release notes.

### 5. Wait for the release to be created

Poll until the GitHub release exists:

```bash
gh release view v<new_version>
```

Wait up to 3 minutes, checking every 15 seconds. If the release doesn't appear, warn the user that the workflow may have failed and ask whether to investigate.

## Important Notes

- Always use `git commit -s` to include the DCO sign-off.
- Never force push to main.
- If the version string is already set to the target version, skip the bump and warn the user.
- If there are uncommitted changes in the working tree (other than the version bump and changelog update), stop and ask the user to commit or stash them first.
- Prefer `make release-push VERSION=v<new_version>` over manual tag and push commands.
- The GitHub Actions workflow handles binary builds, release creation, and current-version release notes extraction.