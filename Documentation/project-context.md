# Arc.Go project context

This is repository-owned project guidance, read through the shared corpus's
**Project-Specific Instructions** section. Arc.Go is a framework library, not
an application; do not impose application vertical slices or C# test tooling.

## Required guidance

For all Go/module work, read [Go rules](../.cratis/ai/rules/go.md) and
[Cratis parity rules](../.cratis/ai/rules/go-cratis-parity.md). Load the relevant
skills under `.cratis/ai/skills/`: `go-library-api-design`, `go-testing`,
`go-errors-and-context`, `go-concurrency`, `go-release-modules`, and
`go-http-handlers`. Apply Go conventions over C# syntax/layout/test conventions;
organization security and release gates still apply.

Optional [third-party Go skills](../.cratis/ai/rules/project/third-party-go-skills.md)
provide pinned JetBrains, spf13, GitHub Copilot and Samber references. Load by task,
not all at once; read their local qualifications. Repository Go/parity rules take
precedence. Each vendored folder retains its license and `UPSTREAM.md`; these are
unmanaged local additions shared with Chronicle.Go, not installer-managed files.

## Product and parity target

- Module: `github.com/cratis/arc.go`; root package: `arc`.
- Purpose: the Go port of Cratis Arc, an HTTP CQRS application framework for
  commands, queries, observable queries, and Chronicle integration.
- Primary reference: `../Arc/Source/DotNET`, its tests, and
  `../Arc/Documentation/http-contract.md`. Client-visible behavior must work with
  the contracts consumed in `../Arc/Source/JavaScript`.
- Consult `../Arc.Kotlin` for cross-language translation experience, not as
  permission to diverge from the C# contract. Never edit sibling ports to
  accommodate Go.
- Maximize API, behavior, and developer-experience parity while using Go idioms.
  Record every deliberate difference and missing surface in
  [the parity map](parity.md); no parity claim without executable evidence.
- Arc.Go's Chronicle integration depends on `github.com/cratis/chronicle.go`.
  This is the intended dependency direction, not a claim that the initial
  scaffold already imports it. Use a published version when implemented, never
  a required local sibling `replace`.
- Keep HTTP CQRS usable without a Chronicle connection. Like C# Arc.Core,
  commands need not emit events; event-sourcing conventions belong to the
  optional integration, not every command/query pipeline.

## Layout and commands

Keep one runtime root module and exactly one tooling-only nested module,
`tools/go.mod` (`github.com/cratis/arc.go/tools`). The `tools/cmd/arc-gen` executable
uses `go/packages` and `go/types`; `golang.org/x/tools` must not become a root
runtime dependency. Tools pin a fetchable runtime version, never a local replace
or workspace. Both modules must build independently with `GOWORK=off`.

Group public packages by capability, keep implementation-only helpers under
`internal/`, and co-locate `_test.go` files. Add directories only when implemented;
do not copy the C# `Source/` namespace layout. Separate HTTP binding/hosting from
command/query execution and the Chronicle adapter. Public examples should compile.
Product documentation belongs in `Documentation/`.

Run from the root using the version in `go.mod` and the matrix in CI:

```sh
go build ./...
go test -race ./...
golangci-lint run
go vet ./...
govulncheck ./...
```

Repeat the Go gates independently from `tools/`, using the root lint configuration.
The root `./...` pattern does not cross the tooling module boundary. Tooling tags
would use `tools/vX.Y.Z`, but publication remains deferred pending
[Fundamentals.Go#16](https://github.com/Cratis/Fundamentals.Go/issues/16); the root
release workflow still publishes only root-module tags.

After authorized dependency changes, run `go mod tidy` in the changed module and
inspect its `go.mod` and `go.sum`. Use `GOWORK=off` to verify independent consumption. Use `httptest` and
wire-contract fixtures for transport behavior, plus bounded Chronicle integration
tests when that adapter exists. The exact required gates and tool pins are in
[CONTRIBUTING](../CONTRIBUTING.md) and `.github/workflows/`; these quick commands
do not replace them.

## Release policy

Exactly one PR label selects intent: `major`, `minor`, `patch`, or `no-release`.
The configured `Cratis/release-action` turns approved release intent into immutable
root-module tags `vX.Y.Z`; the merged PR body supplies the release notes.
While v0.x, breaking changes require a minor bump and migration notes; compatible
fixes use patch. A stable v1 launch needs explicit maintainer approval.

No `/v2` module or import changes without a migration/support plan and revised
release workflow. Never rewrite published tags; publish corrections and use
`retract` when appropriate. Confirm public proxy indexing separately from tagging.
Read [release policy](releases.md) and the `go-release-modules` skill before release
work. Preparing guidance or code does not itself authorize publication.

## Local AI ownership and harnesses

These Go rules, skills, and adapters are repository-owned additions, not entries
in `.cratis/ai.manifest.json`. Do not add managed markers or edit that manifest.
Updates leave unlisted files alone unless a selected corpus asset collides with
their path; such collisions stop the update. Project instructions are a separate
migration case: the CLI splits `rules/project.md` at level-two headings when
`rules/project/` is absent. Keep the entry point heading-free and put concerns in
that directory. Check `cratis ai status` and conflicts before updating, especially
if the upstream corpus later adopts the same paths. Never use `--force` to resolve
that silently.

- Claude reads the rules through `.claude/rules`.
- Codex, OpenCode, and pi reach this document through root `AGENTS.md` and its
  Project-Specific Instructions; the native skill directories expose Go skills.
- Copilot's `go.instructions.md` adapter explicitly loads the same rules/context
  through `.github/instructions`.
- Cursor's local `.mdc` adapters load the same canonical rules/context through
  `.cursor/rules`.

The Kotlin repositories use project-owned rules and a project entry point. Here,
`rules/project.md` points to this canonical document while existing managed root
entry points remain unchanged. Keep one set of facts here, not divergent harness
copies. Confirm instruction discovery in a fresh harness session after updates.
