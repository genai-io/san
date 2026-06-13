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

## Constraints

- Only **patch** (`x.y.Z`) or **minor** (`x.Y.0`) bumps. Never bump major — the
  module path has no `/vN` suffix, so a major bump would violate Go v2+ rules.
- All version tags use the `v` prefix (e.g. `v1.20.0`).

## Workflow

### 0. Pull from upstream

Always pull the latest upstream codebase first:

```bash
git pull upstream main --rebase
```

If there are merge conflicts, resolve them before proceeding.

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

### 2. Verify the working tree is clean

Check for uncommitted changes before touching any files:

```bash
git status --short
```

If there are any modified or untracked files (other than the version bump and changelog update this workflow will create), **stop**. Ask the user to commit or stash them first. Do not proceed until the tree is clean.

### 3. Update the changelog

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

**Contributor attribution:** Every changelog entry must include the author's GitHub handle and a link to the PR or commit, matching the existing format:

```markdown
- Description of change ([@username](https://github.com/username) in [#NNN](https://github.com/genai-io/san/pull/NNN))
```

For direct commits without a PR, use the commit hash:

```markdown
- Description of change ([@username](https://github.com/username) in [abcdef1](https://github.com/genai-io/san/commit/abcdef1))
```

**Exclude:** OWNERS updates, dependabot bumps, and other purely administrative chore commits. Do not list them in the changelog.

Write only the current version section in `CHANGELOG.md`. Do not pass the entire file as manual release notes later; the GitHub Actions workflow extracts the current version section automatically.

### 3. Bump the version in source code

Update the version string in `cmd/san/main.go`:

```go
var version = "<new_version>"
```

### 4. Commit and push

Stage the version bump and changelog update, then commit with sign-off:

```bash
git add cmd/san/main.go CHANGELOG.md
git commit -s -m "chore: bump version to <new_version>"
```

**If the canonical repo is `upstream` and its `main` is protected**, pushing
directly will fail. Use this PR-based path instead:

a. Push the commit to the fork (`origin`):
   ```bash
   git push origin main
   ```

b. Create a PR from fork `main` to upstream `main`:
   ```bash
   gh pr create --base main --head <fork-owner>:main \
     --title "chore: bump version to <new_version>" \
     --body "Bump code version..."
   ```

c. Wait for required CI checks (DCO, test, lint) to pass on the PR.

d. **Check DCO.** If the DCO check fails on upstream commits that lack a
   sign-off (common after rebasing onto upstream), add sign-offs to all PR
   commits:
   ```bash
   git rebase --signoff upstream/main
   git push --force-with-lease origin main
   ```

e. Merge the PR (this repo requires squash-merge):
   ```bash
   gh pr merge <number> --squash --subject "chore: bump version to <new_version>"
   ```

f. Pull the merged result, tag, and push the tag to upstream:
   ```bash
   git fetch upstream && git checkout main && git reset --hard upstream/main
   git tag v<new_version>
   git push upstream v<new_version>
   ```

**If pushing directly to upstream is allowed**, use the Makefile helper:

```bash
make release-push VERSION=v<new_version>
```

This target validates that the working tree is clean, the tag does not already
exist, and `CHANGELOG.md` contains the matching section before it pushes `main`
and the tag.

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
- If the code version already matches the target and the CHANGELOG already contains a matching section (i.e., a prior release attempt was committed but never tagged), skip the bump and changelog steps. Proceed directly to verifying a clean tree, then commit any outstanding files, tag, and push.
