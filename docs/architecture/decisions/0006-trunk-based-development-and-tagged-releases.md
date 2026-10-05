# 0006. Trunk-based development and tagged releases

- Status: Accepted. In effect from 2026-10-05, after phase 1 merged to
  `master`. The release workflow and tag ruleset are not built yet
  ([TODO 0.5](../../../TODO.md)).
- Date: 2026-10-05

## Context

Phase 1 (DB collector) was delivered on a long-lived phase branch,
`db-collectors`. Each TODO item had its own `feature/*` branch and pull request
into that branch, often worked by agents in parallel, and one pull request
merged the whole phase into `master` (#16).

That worked for one phase at a time but does not scale:

- Several phases running at once on separate phase branches drift apart. Shared
  files (`TODO.md`, the roadmap, the Helm chart, CI, contracts) conflict at the
  end, and work built on a stale `master` has to be redone.
- The phase branch fell behind `master` and had to be caught up by hand.
- Once `master` feeds production, merging a whole phase means one large deploy
  and a rollback that undoes the whole phase.

Nothing is versioned today. The chart says `0.1.0`, images are tagged with a
content hash for kind, and production (ADR 0005) will run images pinned by
digest, but no rule says which commit becomes a release.

## Decision

| Concern | Choice | Why |
| --- | --- | --- |
| Branching | **Trunk-based development.** Short-lived `feature/*` branches from `master`, one per TODO item, merged back by pull request. No phase or integration branches | Work is integrated continuously, not at the end of a phase; parallel work across phases is limited only by real dependencies. |
| Merging | Merge commits only; head branches are deleted on merge; `master` requires a pull request and passing CI (`test`, `Helm chart`, `kind end-to-end`) | Merge commits keep each branch's commits intact, which the changelog reads. |
| Unfinished work | Merged but not reachable: not wired in yet, or behind a configuration flag. Schema changes follow expand, then contract | `master` stays releasable at every commit; ADR 0003 (risk 5) notes that a rollback does not reverse schema changes. |
| Versions | One [Semantic Versioning](https://semver.org/) version for the repository: images, chart and changelog. `0.y.z` until the first production release. The first release is `v0.1.0`, phase 1 | One number answers "what is running"; the chart is already `0.1.0`. |
| Releases | A `vX.Y.Z` tag on `master`. The tag builds the multi-arch images, tags them with the version and records their digests in the release notes | A release is an explicit, immutable point on `master`, not whatever `master` holds. |
| Changelog | [`CHANGELOG.md`](../../../CHANGELOG.md) in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, generated from [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) by **release-please**: it keeps an open release pull request with the next version and changelog entries, and merging it creates the tag and the GitHub release | No hand-edited changelog section for parallel branches to conflict on; tags are created through a reviewed pull request, which fits a protected `master`. |
| Registry | **Amazon ECR** for production, as in ADR 0005. Until the AWS account exists, release images go to **GitHub Container Registry** (`ghcr.io/gdpais/heartbeat-*`) | The repository is public, so GHCR is free and pullable from kind, which makes the Argo CD rehearsal on kind [18] realistic without AWS. |
| Promotion | Argo CD deploys from `heartbeat-deploy` (ADR 0005). Promoting a release is a commit there that sets the image digest for an environment: dev, then staging, then production | Every deploy is a reviewed commit that names an exact image; rollback is `git revert`. |
| Process | Contribution rules live in [`CONTRIBUTING.md`](../../../CONTRIBUTING.md) | One home for how to branch, commit, test and open pull requests, for people and agents. |

Considered and not chosen:

- **Phase branches for every phase.** Kept `master` at "last finished phase",
  but a finished phase is now marked by a tag instead, without the drift.
- **GitFlow** (`develop`, `release/*`, `hotfix/*`). Built for shipping several
  supported versions; Heartbeat runs one deployment.
- **A hand-written changelog.** Every parallel branch would edit the same
  `Unreleased` section and conflict.
- **Squash merging.** Rewrites each branch into one new commit, which breaks
  any long-lived branch that is merged again and hides the commits the
  changelog reads.

## Consequences

- Every commit on `master` must pass CI and be safe to release. Large features
  land in several small pull requests, the unfinished parts switched off.
- Commit messages are release input: a `feat` commit bumps the minor version,
  `fix` the patch, and `!` or a `BREAKING CHANGE:` footer marks a breaking
  change, which bumps the major version (the minor while `0.y.z`, with
  release-please's `bump-minor-pre-major`). Commits that are not Conventional
  Commits do not appear in the changelog.
- Tags created with the default `GITHUB_TOKEN` do not start other workflows, so
  the image build runs in the release-please workflow, gated on a release being
  created.
- `v*` tags are protected by a tag ruleset: no updates or deletions. A bad
  release is fixed by a new patch release, never by moving a tag.
- The `db-collectors` branch, its ruleset and its CI push trigger are retired.
- ECR publishing in production delivery [18] reuses the same release workflow
  with a second registry; no second build pipeline.
- Documentation still publishes from `master` on every push, so docs can be
  ahead of the latest release.
