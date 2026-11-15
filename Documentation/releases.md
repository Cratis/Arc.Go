---
title: Go release policy
description: Arc.Go is experimental v0 with root tags v0.1.0 to v0.1.3; learn PR release labels, v0.x versioning rules, and how publication verifies tags and the Go proxy.
---

Arc.Go is experimental v0 software. The root module `github.com/cratis/arc.go` has the Git tags `v0.1.0`, `v0.1.1`, `v0.1.2` and `v0.1.3`. Releases remain v0.x until an approved stable launch, and APIs may change between minor releases.

## Release intent

Every PR carries one release label: `major`, `minor`, `patch`, or `no-release`. The repository's `verify-pull-request-policy.yml` workflow calls the shared Cratis policy with release intent enabled. While v0.x, use minor for breaking API changes and patch for compatible fixes. Describe breaking changes explicitly in the release notes. Dependabot PRs use `no-release`; a dependency change that needs publication requires a deliberate release-bound maintainer change.

The `verify-semver-label.yml` workflow adds the Go major-release policy: a `major` label fails the check while `GO_RELEASE_MAJOR_CEILING` is 0 (the default), because it would publish v1.0.0.

The merged PR body becomes the GitHub Release notes verbatim. Use the [contribution guide](../CONTRIBUTING.md) and PR template, and keep verification and review notes in a PR comment.

## Publication

A push to main runs `publish.yml`. It runs the Go build workflow and the Markdown verification, then prepares a release from the merged PR's label and body. The release action creates the immutable `vX.Y.Z` tag and GitHub Release only after its job succeeds. A merge labeled `no-release` publishes nothing. Dependent jobs read back the release and tag target, fetch the exact version through `proxy.golang.org`, verify the module archive, and request its pkg.go.dev page. Documentation rendering is asynchronous.

There is no separate registry upload or Go publishing credential. GitHub tags are Go module versions; the workflow uses its scoped `GITHUB_TOKEN` for GitHub writes. Keep the canonical lowercase root module path, `github.com/cratis/arc.go`.

Publish runs queue first-in first-out (`queue: max`) and are never cancelled in flight, so merging another release-bound PR while a run is active does not lose its release.

## Nested modules

These directories are separate Go modules in this repository, each with its own `go.mod`:

| Directory | Module path |
| --------- | ----------- |
| `tools/` | `github.com/cratis/arc.go/tools` |
| `integrations/chronicle/` | `github.com/cratis/arc.go/integrations/chronicle` |
| `integrations/mongodb/` | `github.com/cratis/arc.go/integrations/mongodb` |
| `recipes/` | `github.com/cratis/arc.go/recipes` |

`publish.yml` reads only the root `go.mod`, creates only root `vX.Y.Z` tags and indexes only the root module. No nested-module tags exist in this repository. A root tag does not release a nested module. This page does not describe how, or whether, the nested modules are published.

## Major versions

`GO_RELEASE_MAJOR_CEILING` defaults to 0. Set it to 1 only for a maintainer-approved v1 launch and refresh the PR policy check. A major release must be reviewed and merged by a human. Values above 1 are rejected: Go v2+ requires a `/vN` module path, changed imports, and a separately reviewed release design. The current workflow does not publish prereleases or nested modules.

## Recovery

If proxy indexing fails after publication, rerun the failed jobs. A full rerun resolves the existing stable release for the same commit and indexes that tag without bumping again. Investigate any conflicting tag or release; never move or delete a published tag. Correct a bad release with a new version and, where appropriate, a Go `retract` directive.

Central Cratis documentation publication requires registering this repository in Cratis/Documentation and setting the repository variable `DOCUMENTATION_ENABLED` to `true`; it is independent of Go module indexing.
