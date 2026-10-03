# Contributing to Arc for Go

Thank you for helping build the Cratis Arc framework for Go. This repository is in early development: it provides foundation contracts, command, snapshot and observable-query pipelines, HTTP/SSE/WebSocket hosting, `arctest` scenarios, an experimental model-bound adapter generator, and an optional Chronicle integration. Observable wire contracts have real-listener evidence; actual locked JavaScript/browser execution, generated observable adapters, database watches and full product parity remain unverified or unsupported. Discuss larger changes before implementation, and document only capabilities that exist and have been verified.

The [Cratis contribution guide](https://github.com/Cratis/.github/blob/main/contributing.md) and [code of conduct](https://github.com/Cratis/.github/blob/main/CODE_OF_CONDUCT.md) apply.

## Before you start

- Open or identify a GitHub issue for the work; keep changes focused on it.
- This is a library, not an application. Do not add application-style domains or UI structure to the package.
- Match observable Arc behavior idiomatically in Go, rather than mechanically translating another language implementation. The [Arc HTTP contract](https://github.com/Cratis/Arc/blob/main/Documentation/http-contract.md) is a reference when implementing HTTP behavior.
- Do not claim host support, HTTP contract conformance, or feature parity without corresponding tests. Chronicle integration is an optional nested module, not a root runtime dependency.

## Layout and setup

The runtime is the root module, `github.com/cratis/arc.go`, with package `arc`. The supported nested modules are `tools/go.mod` (`github.com/cratis/arc.go/tools`), containing the `arc-gen` artifact generator, and `integrations/chronicle/go.mod` (`github.com/cratis/arc.go/integrations/chronicle`), containing the Chronicle integration and SDK adapter. The additional exact exception is `integrations/mongodb/go.mod` (`github.com/cratis/arc.go/integrations/mongodb`), providing borrowed bindings, BSON codecs and bounded snapshot rendering, not watches. Their tooling, Chronicle SDK, and MongoDB driver dependencies must not enter the runtime dependency graph. All nested modules pin fetchable runtime revisions and build independently with `GOWORK=off`.

Product documentation lives in `Documentation/`. Add packages and examples only as implementation needs them; use lowercase package directories, co-located `_test.go` files, and compiling `Example` tests for public usage. Root releases remain `vX.Y.Z`. Future nested-module releases need independent `tools/vX.Y.Z`, `integrations/chronicle/vX.Y.Z`, and `integrations/mongodb/vX.Y.Z` tags; their tagging and publication are deferred pending the pattern in [Fundamentals.Go#16](https://github.com/Cratis/Fundamentals.Go/issues/16).

Install Go 1.26 or later, golangci-lint v2.14.0, actionlint v1.7.12, ShellCheck, and markdownlint-cli2. CI tests Go 1.26 and 1.27, including the latest patches; golangci-lint must be built with a Go version at least as new as the code it analyzes.

## Verify your change

Run from the repository root, with each supported Go toolchain where applicable:

```sh
export GOWORK=off
export GOTOOLCHAIN=local
go mod download
go mod verify
go build ./...
go vet ./...
python3 scripts/check-no-container.py
go test -count=1 -timeout=2m ./...
go test -race -count=1 -timeout=3m ./...
golangci-lint run
go mod tidy
git diff --exit-code -- go.mod go.sum
actionlint -color
npx markdownlint-cli2 '*.md' 'Documentation/**/*.md' 'examples/**/*.md' 'ContractTests/fixtures/**/*.md' 'ContractTests/observables/*.md' '.github/ISSUE_TEMPLATE/*.md' '.github/pull_request_template.md' '!AGENTS.md' '!CLAUDE.md'
```

Race detection requires a supported platform and a C compiler. Run govulncheck with Go 1.27:

```sh
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
govulncheck ./...
```

Repeat the Go build, vet, ordinary and race tests, lint, tidy-diff, and vulnerability checks from `tools/`, also with `GOWORK=off` and `GOTOOLCHAIN=local`. Use `golangci-lint run --config=../.golangci.yml` there. Regenerate the checked-in consumer adapters from `tools/` with `go run ./cmd/arc-gen -dir .. ./ContractTests/generatedconsumer`; verify them with the same command plus `-check` before the package pattern. Generator tests also compile and execute independent consumers against the pinned runtime version.

Format all Go source in all four modules with `gofmt`; no source files should appear in `gofmt -l` output. After `go mod tidy -diff`, also check `git status --short -- go.mod go.sum tools/go.mod tools/go.sum integrations/chronicle/go.mod integrations/chronicle/go.sum integrations/mongodb/go.mod integrations/mongodb/go.sum` for untracked manifests. Commit `go.sum` when dependencies require it. Do not add other nested modules, local `replace` directives, or personal `go.work` files: all four modules must build without sibling checkouts.

Repeat these Go checks from `integrations/chronicle/` with `--config=../../.golangci.yml` for lint. CI also runs its tagged kernel contracts and taskboard sample against `cratis/chronicle:19.29.4-development`; set `CHRONICLE_INTEGRATION_CONNECTION_STRING` for those tests.

Repeat the native build, vet, test, race, tidy-diff, verify, lint, and vulnerability gates from `integrations/mongodb/`, with `--config=../../.golangci.yml` for lint. Its ordinary tests require no database. Run `python3 scripts/check-module-boundaries.py` from the root to verify the exact manifest allowlist and resolved runtime dependency boundary. The independent Linux Go 1.27 live-provider lane runs `python3 integrations/mongodb/scripts/replica-set-tests.py --state-file "$PWD/.ai-work/keep/mongodb-provider-local-unique.json"` from the root with a fresh state path and Docker available. It owns one digest-pinned MongoDB 8.0.15 replica-set container, uses a Docker-assigned loopback port and external direct URI, budgets startup/tests/diagnostics-and-cleanup at 90/180/15 seconds, and removes only its verified container ID/token. Missing prerequisites/URI or failpoint support fail the required lane, not skip. Run `python3 -B -m unittest discover -s integrations/mongodb/scripts -p 'test_*.py' -v` and `python3 integrations/mongodb/scripts/check-doc-snippet.py` for offline harness/example regressions; the live job also lints integration-tagged contracts. Synthetic release tests do not prove real Chronicle sink layout or compliance.

Hosted CI also runs the ordinary build, vet, and tests on macOS and Windows. Workflow lint invokes ShellCheck when it is available. Foundation behavioral and wire-fixture tests run without external services. They are not HTTP integration tests; add explicitly bounded integration checks before claiming HTTP contract conformance. CodeQL runs separately in GitHub Actions.

## Zero-container guarantee

Keep plain constructors, closures, and explicitly supplied resources first-class.
The [no-container example](examples/nocontainer/main.go) runs authorization,
validation, and execution-scope flows without importing any Fundamentals
`dependencyinjection` package. Its tests cover successful output, authorization
denial, validation failure, and resource cleanup.

`python3 scripts/check-no-container.py` uses `go list -deps -json ./...` without
`-test` to check every runtime package in the root module. None may depend on
`github.com/cratis/fundamentals.go/dependencyinjection/container`; container imports
in tests do not affect that graph. Only explicitly reviewed DI-demonstrating
examples may be excluded in the script (currently none). The check also rejects
any direct DI import in the no-container example, including its tests.

The standard-library-only DI **contracts** may appear transitively through
`execution`, including from authorization and validation. The guarantee excludes
an imposed container implementation, not these interfaces. Do not require a
container merely to use a public entry point. CI runs this check in the module
hygiene job and compiles/tests the example with all other packages. See
[dependency injection and operation resources](Documentation/backend/go/core/dependency-injection.md)
for plain wiring and optional container integration.

## Conventions

- Use American English and idiomatic Go, including context cancellation and explicit error handling.
- Start source files with the Cratis copyright and MIT license header, as in `doc.go`.
- Let `gofmt` control Go formatting; use `.editorconfig` for other files.
- Document exported APIs and update `Documentation/` with compiling examples when those APIs exist.

## Pull requests and releases

- Use focused conventional commits and merge commits; do not squash, rebase, or force-push shared history.
- Apply exactly one release-intent label: `major`, `minor`, `patch`, or `no-release`. Setup-only changes use `no-release`; Dependabot uses `no-release` too.
- Keep the PR body user-facing: optional `## Summary`, then only applicable `## Added`, `Changed`, `Fixed`, `Removed`, `Security`, or `Deprecated` sections, with concise bullets. End a delivered issue's bullet with `(#123)`; use `(part of #123)` if it stays open. Delete placeholders and unused sections, use absolute links, and put test/review notes in a PR comment.
- The PR body is published verbatim as release notes. The first minor release becomes v0.1.0. During v0.x, use minor for breaking experimental API changes and describe the break explicitly; use patch for compatible fixes.
- A major release requires human approval. `GO_RELEASE_MAJOR_CEILING` defaults to 0, blocking an accidental v1 launch. A maintainer can set it to 1 for an approved v1 release; v2+ requires `/vN` module/import paths and a revised workflow.
- Wait for Publish to finish before merging the next release-bound PR. Tags are immutable; never delete or move a released version. See [release policy](Documentation/releases.md).

## AI-assisted contributions

Managed Cratis AI rules and harness adapters are not hand-edited. Shared improvements belong in [Cratis AI](https://github.com/Cratis/AI); project-specific guidance belongs under `.cratis/ai/rules/project/`.

Plans, scratch files, and work records belong only in the ignored `.ai-work/` directory and are never committed. Durable follow-ups belong in GitHub issues.

## Security

Do not report vulnerabilities in public issues. Follow [SECURITY.md](SECURITY.md).
