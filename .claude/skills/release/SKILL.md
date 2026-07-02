---
name: release
description: Guide through cutting a new pgdba-cli release — version bump, PostgreSQL compatibility testing, tagging, and confirming Homebrew/Scoop publication. Use when the user asks to cut/prepare/ship a release, or to bump the version.
argument-hint: [version, e.g. 0.4.0]
---

# Release checklist for pgdba-cli v$0

Walk through each step below in order. Confirm with the user before any action that pushes
to a shared ref (tag push, `main`) or otherwise triggers external side effects.

`main` is a **protected branch on GitHub** — direct pushes are rejected outright (requires
a PR + the `build` status check). This was discovered the hard way while cutting v0.3.0:
a direct `git push origin main` for a changelog-only commit failed with
`GH006: Protected branch update failed`. Any change touching `main` — even a one-line
CHANGELOG.md edit — goes through a branch + PR + explicit merge confirmation, never a
direct commit.

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

## 3. Update CHANGELOG.md (via a PR)

- Create a small branch for this, e.g. `docs/changelog-vX.Y.Z` — do not attempt to commit
  straight to `main` (see the protected-branch note above).
- Move the relevant entries from `## [Unreleased]` into a new `## [X.Y.Z] - YYYY-MM-DD`
  section (Keep a Changelog format — `### Added`/`### Fixed`/`### Changed` etc.).
- Add the new version's compare link at the bottom
  (`[X.Y.Z]: https://github.com/liciomatos/pgdba/compare/vPREV...vX.Y.Z`) and update
  `[Unreleased]` to compare from the new tag.
- Commit, push the branch, open a PR against `main` (`gh pr create --base main`), wait for
  the `build` check to pass, then **stop and confirm with the user before merging** — same
  bar as a tag push, this is still a change to shared history.
- After the merge, sync local `main` (`git checkout main && git pull origin main`) before
  moving on — tagging from a stale local `main` will tag the wrong commit.
- This is a curated, human-readable summary — not a raw commit dump. GoReleaser's
  auto-generated GitHub Release notes (from `feat:`/`fix:` commit subjects) still get
  created separately and are fine to stay terse/mechanical; CHANGELOG.md is the one meant
  to read well.

## 4. Run the PostgreSQL compatibility matrix

- `make test-pg-matrix` locally (tests PG13–18, ~5x slower than the normal test loop —
  requires Docker/Podman with working `testcontainers-go` support).
- If local Docker isn't usable, trigger `.github/workflows/pg-compat.yml` manually via
  `gh workflow run pg-compat.yml` and wait for it to go green instead.
- Do not proceed to tagging if any supported version fails.
- **If every version fails with the same error**, it's almost certainly a shared test-infra
  problem, not a real per-version compatibility regression — check `TestMain` in
  `util/db_integration_test.go` first before assuming any `Fetch*` code is broken (this
  happened once: `pg_stat_statements` wasn't preloaded at container startup, failing
  identically across all 6 versions — see commit `706bbc9`).

## 5. Tag and push

- `git tag vX.Y.Z` on `main`.
- **Stop and confirm with the user before running `git push origin vX.Y.Z`** — this single
  push triggers real, hard-to-reverse external effects:
  - `.github/workflows/release.yml` → GoReleaser cross-compiles binaries, creates the
    GitHub Release with the auto-generated changelog, and **automatically publishes to
    the Homebrew tap (`liciomatos/homebrew-tap`) and Scoop bucket
    (`liciomatos/scoop-bucket`)** via `TAP_GITHUB_TOKEN` — no manual step exists for this.
  - `.github/workflows/pg-compat.yml` runs the full PG13–18 matrix again, in parallel.

## 6. Verify

- `gh run list --limit 5` (or the GitHub Actions UI) — confirm both workflows go green.
- Confirm the GitHub Release was created with all expected artifacts (linux/darwin/windows
  × amd64/arm64) — `gh release view vX.Y.Z`.
- Confirm a commit/PR landed in `liciomatos/homebrew-tap` and `liciomatos/scoop-bucket`.
  If it didn't, the most likely cause is `TAP_GITHUB_TOKEN` expiring — check that secret
  before assuming GoReleaser itself is broken.

## 7. Announce (optional)

- If the user wants an announcement post, offer to draft one from the CHANGELOG.md entry
  just added for this version.
