---
name: release
description: Guide through cutting a new pgdba-cli release — version bump, PostgreSQL compatibility testing, tagging, and confirming Homebrew/Scoop publication. Use when the user asks to cut/prepare/ship a release, or to bump the version.
argument-hint: [version, e.g. 0.4.0]
---

# Release checklist for pgdba-cli v$0

Walk through each step below in order. Confirm with the user before any action that pushes
to a shared ref (tag push) or otherwise triggers external side effects — never push a tag
without explicit confirmation, same as the project's rule against pushing to `main`.

## 1. Confirm branch state

- `git status` — ensure `main` is up to date and the working tree is clean.
- `git log --oneline $(git describe --tags --abbrev=0)..main` — see what's shipping in
  this release. Every `feat:`/`fix:` commit here will appear in the auto-generated
  changelog (see `.goreleaser.yaml`'s `changelog:` config, which excludes `chore:`/`ci:`
  and merge commits).

## 2. Confirm the version number

- Semver: any `feat:` commit since the last tag → bump minor. Only `fix:`/`docs:`/`chore:`
  → bump patch. A breaking change → bump major (rare — flag it explicitly to the user if
  you see one, don't decide silently).
- If `$0` wasn't given, propose a version based on the above and ask the user to confirm.

## 3. Run the PostgreSQL compatibility matrix

- `make test-pg-matrix` locally (tests PG13–18, ~5x slower than the normal test loop —
  requires Docker/Podman with working `testcontainers-go` support).
- If local Docker isn't usable, trigger `.github/workflows/pg-compat.yml` manually via
  `gh workflow run pg-compat.yml` and wait for it to go green instead.
- Do not proceed to tagging if any supported version fails.

## 4. Tag and push

- `git tag vX.Y.Z` on `main`.
- **Stop and confirm with the user before running `git push origin vX.Y.Z`** — this single
  push triggers real, hard-to-reverse external effects:
  - `.github/workflows/release.yml` → GoReleaser cross-compiles binaries, creates the
    GitHub Release with the auto-generated changelog, and **automatically publishes to
    the Homebrew tap (`liciomatos/homebrew-tap`) and Scoop bucket
    (`liciomatos/scoop-bucket`)** via `TAP_GITHUB_TOKEN` — no manual step exists for this.
  - `.github/workflows/pg-compat.yml` runs the full PG13–18 matrix again, in parallel.

## 5. Verify

- `gh run list --limit 5` (or the GitHub Actions UI) — confirm both workflows go green.
- Confirm the GitHub Release was created with all expected artifacts (linux/darwin/windows
  × amd64/arm64) — `gh release view vX.Y.Z`.
- Confirm a commit/PR landed in `liciomatos/homebrew-tap` and `liciomatos/scoop-bucket`.
  If it didn't, the most likely cause is `TAP_GITHUB_TOKEN` expiring — check that secret
  before assuming GoReleaser itself is broken.

## 6. Announce (optional)

- If the user wants a changelog/announcement post, offer to draft one from the same
  `feat:`/`fix:` commits GoReleaser's changelog used.
