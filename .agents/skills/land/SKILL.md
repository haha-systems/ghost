---
name: land
description: >-
  Land the current Ghost change through a verified pull request into trunk. Invoke only when the
  user explicitly requests landing, including through Delta's Land Changes action; do not invoke
  merely for review, preparation, verification, or installation.
metadata:
  delta-action: land
---

# Land Ghost changes

This invocation is already the user's explicit request to land the relevant change. Do not ask for
merge permission again. Carry the workflow through preparation, verification, publication, merge,
and destination verification unless a genuine blocker requires the user.

## Scope and safety

- Work from the current Delta worktree and identify the change requested by the thread.
- Preserve unrelated edits. Never stage, commit, discard, or overwrite unrelated work.
- The publication remote is `origin`; never publish through the `local` backlink.
- Resolve conflicts automatically when the intended result is clear. Preserve both the requested
  change and unrelated upstream work, rerun all checks afterward, and stop for user input only when
  the resolution is ambiguous or unsafe.
- Do not force-push, rewrite shared history, bypass checks, change repository rules, publish
  packages, or expose credentials.
- Use non-interactive commands. Prefix any Git command that could open an editor with
  `GIT_EDITOR=true`.

## Preflight

1. Inspect `git status`, the complete diff, current `HEAD`, configured remotes, and the thread
   context. Confirm the intended files and reject accidental generated files, secrets, binaries,
   or unrelated edits.
2. Fetch `origin` and verify its default branch with `git ls-remote --symref origin HEAD`. The
   expected destination is `trunk`; if the remote default changed, use the verified default and
   explain the change rather than assuming.
3. Verify `gh auth status` for the repository host without printing credential values.
4. Ensure the intended change can be represented as a topic branch based on the latest
   `origin/trunk`. Create a uniquely named `land/...` branch. Rebase or transplant the intended
   work onto the latest destination without carrying unrelated work. Resolve clear conflicts
   automatically and stop on ambiguous conflicts.

## Required local verification

Run all checks against the exact candidate commit:

```sh
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
go build ./cmd/ghost
! grep -q "^replace github.com/haha-systems/qac" go.mod
```

These commands mirror `.github/workflows/ci.yml` and the repository targets in `Makefile`.
Formatting failures may be corrected with the repository's `make fmt` target, but review the
resulting diff and ensure it changes only intended Go files before continuing. After any code,
conflict-resolution, or formatting change, rerun the full verification set.

## Commit and publish

1. Stage only the intended paths and inspect the staged diff.
2. Create a concise conventional commit describing the coherent change. Do not amend or rewrite
   existing shared commits.
3. Push the topic branch to `origin` without force.
4. Open a pull request with `gh pr create --base trunk --head <branch>`. Summarize the behavior,
   important design decisions, and verification performed. Do not invent issue references.

The CI workflow is triggered by pull requests and executes the same Go tests, vet, build,
dependency-replacement guard, and formatting check listed above
(`.github/workflows/ci.yml`). A push to `trunk` is not a substitute: the workflow's push trigger
currently names `main`.

## Verify and merge

1. Record the pushed head SHA.
2. Use `gh pr checks --watch --fail-fast <pr>` or equivalent GitHub CLI queries to wait for every
   check attached to that exact pull-request head SHA. Confirm the head SHA has not changed while
   checks run.
3. Treat pending, skipped-required, missing, failed, canceled, or unverifiable checks as blockers.
   Do not merge based only on local checks or an earlier commit's CI.
4. When every check passes, squash merge with
   `gh pr merge <pr> --squash --delete-branch=false`. Do not use admin bypass.
5. Fetch `origin` again and verify through both GitHub's PR state and Git ancestry/API data that
   the pull request's squash commit is reachable from the verified destination branch. A prepared
   commit, pushed branch, open PR, or passing CI is not landing success.

## Report the outcome

Report the destination branch, short squash-commit SHA, pull-request URL, and verified CI run URL.
Never invent a URL; omit any link that cannot be verified.

When running in a subthread and `report_subthread_status` is available:

- After verified landing, call it with `status: "success"`, a short title such as
  `Landed on trunk`, and one short description linking the verified commit and CI run.
- For a genuine blocker or failed landing attempt, call it with `status: "failure"`, a concise
  blocker title, and one short description linking available evidence and explicitly stating that
  the change was not landed.
- Do not report routine progress or skill installation. If a recoverable failure is fixed, continue
  and report the updated verified outcome.

Otherwise report the same result directly in the current conversation.
