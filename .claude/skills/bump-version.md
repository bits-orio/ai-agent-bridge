# Bump Version

## When to use
When the user asks to bump the version, release a new version, or after the version in `companion-mod/info.json` has been changed.

This project ships two things under one version: the **Go service** (GitHub release binaries) and the **companion Factorio mod** (`companion-mod/`, optionally the mod portal). `companion-mod/info.json`'s `version` is the single source of truth, and the release tag must be `v<that version>` (the release workflow fails the build if they disagree).

## Steps

1. **Bump the version in `companion-mod/info.json`** if not already done. **Default to a patch bump** (e.g. `0.1.0` → `0.1.1`). Only do a minor or major bump if the user explicitly asks for one, do not infer from the diff.

   Semver components (https://semver.org/), given a version `MAJOR.MINOR.PATCH`:
   - **PATCH**, backwards-compatible bug fixes. Default. Increment the third number; reset nothing. `0.1.0` → `0.1.1`.
   - **MINOR**, backwards-compatible new functionality. Only when the user requests it. Increment the second number; reset patch to 0. `0.1.1` → `0.2.0`.
   - **MAJOR**, incompatible / breaking changes (save-format breakage, removed commands, a changed `aab-rpc-v1` reply shape, etc.). Only when the user requests it. Increment the first number; reset minor and patch to 0. `0.2.0` → `1.0.0`. The protocol, the remote interface `ai-agent-bridge-v1` and the probe `agent_tools_v1` are frozen, a breaking change to any of them ships under a new name beside the old one, not as a silent major bump.

2. **Verify user-facing docs** are still accurate for the changes since the last release. Check these surfaces:
   - `README.md` (repo root), the external-facing description for GitHub readers and operators.
   - `companion-mod/README.md`, the integrator API and mod install notes (also what mod portal viewers see), once it exists.
   - `TESTING.md`, `PLAN.md`, verification steps, phases, open questions.

   All must be **correct**: no claim that contradicts current behavior. If a recent commit added a tool, an artifact shape, or changed the protocol, update the relevant files. Do not invent or expand claims to features that have not been tested. Show any doc edits to the user for approval before committing.

3. **Generate a changelog entry** at the top of `companion-mod/changelog.txt` (it lives with the mod so the Factorio portal and the in-game changelog pick it up; the release workflow also extracts GitHub release notes from it):
   - Determine the previous version's git tag (format: `v<old_version>`). If no tag exists, use `git log` to find commits since the last changelog entry.
   - Collect the diff: `git log --pretty=format:"- %s" v<old_version>..HEAD` (exclude "Bump version" commits).
   - Write a new entry at the **top** of `companion-mod/changelog.txt` following the existing format exactly:
     ```
     ---------------------------------------------------------------------------------------------------
     Version: <new_version>
     Date: <YYYY-MM-DD>
       Features:
         - ...
       Changes:
         - ...
       Bugfixes:
         - ...
     ```
   - Only include sections (Features, Changes, Bugfixes) that have entries. Categorize each commit appropriately. Reword commit messages into clear, user-facing descriptions, don't just paste raw commit subjects.
   - Show the draft entry to the user for approval before writing it.

4. **Commit the version bump**: stage `companion-mod/info.json`, `companion-mod/changelog.txt`, and any doc edits from step 2. Commit with message: `Bump version to <new_version>` (or `Release <new_version>: <one-line summary>` if substantial doc/feature work shipped, match the recent commit history's style).

5. **Release** (when the user asks): push the bump commit, then run `./tools/release.sh`. The script verifies the changelog entry, creates and pushes `v<new_version>`, and the GitHub Actions workflow takes over (build companion-mod zip + cross-platform service binaries → GitHub release → mod portal upload → mod portal sync).
   - The mod-portal upload is **optional**, the step is skipped if `FACTORIO_API_KEY` is unset, so the GitHub release always publishes.
   - If the mod-portal upload step fails (portal outage, etc.), the GH release and tag remain. Re-run the upload via the **Upload to Mod Portal** workflow (Actions tab → workflow_dispatch). The upload script is idempotent, it noops if the version is already published.
   - Required secret on the GitHub repo (optional): `FACTORIO_API_KEY` (scope: ModPortal: Upload Mods for releases, ModPortal: Edit Mods for `sync_portal_details.sh`).
   - **First mod-portal release only:** the Factorio upload API adds a *release* to an *existing* mod page, create the `ai-agent-bridge` mod page once on mods.factorio.com before the portal upload can succeed.
