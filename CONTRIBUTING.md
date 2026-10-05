# Contributing to Heartbeat

How to branch, commit, test and open pull requests, for people and coding
agents alike. The reasoning behind these rules is in
[ADR 0006](docs/architecture/decisions/0006-trunk-based-development-and-tagged-releases.md).

## Workflow at a glance

1. Pick an item from [TODO.md](TODO.md) whose dependencies are done.
2. Branch from an up-to-date `master`: `feature/<todo#>-<slug>`.
3. Commit with Conventional Commits; keep tests and docs in the same branch.
4. Merge the latest `master` in, run the checks, open a pull request to
   `master`.
5. CI passes, the pull request is merged with a merge commit, and the branch is
   deleted.

`master` is protected: every change arrives by pull request and must pass CI.

## Branches

Name branches `feature/<todo#>-<slug>`, with the TODO section number and a
few words, for example `feature/2.7-endpoint-security` or
`feature/3.3-outsystems-parsers`. Work without a TODO item uses
`feature/<slug>`.

```bash
git switch master && git pull
git switch -c feature/3.3-outsystems-parsers
```

Keep branches short-lived: days, not weeks. A large item lands in several
pull requests. Unfinished parts are merged switched off: not wired in yet, or
behind a configuration flag. Every commit on `master` must be safe to release.

Database schema changes follow expand, then contract: add the new column or
table, move the code over, and remove the old one in a later pull request. A
rollback does not undo a migration.

## Working in parallel

Several people or agents can work at once, one branch each, in separate
worktrees:

```bash
git worktree add .claude/worktrees/3.3-outsystems-parsers -b feature/3.3-outsystems-parsers master
```

- Name the worktree after its branch, never a random name, so it is clear what
  it holds. `.claude/worktrees/` is ignored by git; a folder outside the
  repository also works.
- Run in parallel only items that touch different code. Items that change the
  same files, or depend on each other, go one after another.
- Before opening a pull request, and again if `master` moves while it is open,
  merge `master` into the branch and re-run the checks:
  `git fetch && git merge origin/master`. Never rebase or force-push a branch
  that has been pushed.
- When the pull request is merged, remove the worktree with
  `git worktree remove <path>` and delete the local branch with
  `git branch -d <branch>`.

## Commits

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
The changelog and the next version number are generated from them, so a
commit that does not follow the format is left out of the release notes.

```text
<type>(<scope>): <what changed, imperative, lower case>
```

| Type | Use for | Version bump |
| --- | --- | --- |
| `feat` | New behavior a user or operator can see | minor |
| `fix` | A bug fix | patch |
| `perf` | A performance improvement | patch |
| `docs` | Documentation only | none |
| `test` | Tests only | none |
| `refactor`, `style` | Code changes with no behavior change | none |
| `build`, `ci`, `chore` | Build, CI, tooling and housekeeping | none |

The scope is the component: `db-collector`, `otel-gateway`, `config`,
`chart`, `rules`, `dashboards`, `kind-e2e`, `docsite`. Mark a breaking change
with `!` after the scope (`feat(config)!: …`) and explain it in a
`BREAKING CHANGE:` footer.

## Before opening a pull request

Run the checks that cover what you changed; the
[test table](docs/guides/local-development.md#run-tests) lists each command
and what it needs. At minimum:

```bash
make test
make vet
```

Then:

- Update the docs in the same pull request. Each topic has one home (see
  [docs/README.md](docs/README.md)); link to it instead of repeating it.
- Tick the TODO items the pull request completes, and update the
  [roadmap](docs/product/roadmap.md) when a phase changes status.
- Record a decision that shapes the system as a new
  [ADR](docs/architecture/decisions/README.md). Working notes in
  `docs/plans/` and `docs/reviews/` are not tracked.
- Do not edit `CHANGELOG.md` by hand; it is generated at release.

## Pull requests

- Target `master`. Title: what the change does, with the TODO numbers in
  brackets, for example `db-collector: secure the collector endpoints [2.7]`.
- Description: what changed, why, and how it was tested.
- Required checks: `test`, `Helm chart` and `kind end-to-end`.
- Merge with a merge commit; squash and rebase merging are disabled. The
  branch is deleted on merge.

With `git config fetch.prune true`, every fetch also removes the
`origin/*` copies of deleted branches; `git branch -vv` then shows merged local
branches as `gone`, ready for `git branch -d`.

## Releases

A release is a `vX.Y.Z` tag on `master` with [Semantic Versioning](https://semver.org/).
release-please keeps an open release pull request with the next version and its
[CHANGELOG.md](CHANGELOG.md) entries. Merging it tags `master`, publishes the
GitHub release and builds the images for that version. Production runs a
release by its image digest, promoted through Argo CD
([ADR 0005](docs/architecture/decisions/0005-production-delivery-and-operations-defaults.md)).
Tags are never moved or deleted; a bad release is fixed by the next patch
release.

The release workflow is not built yet ([TODO 0.5](TODO.md)); until it is,
there are no tags and no published images.
