# Arc for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/cratis/arc.go.svg)](https://pkg.go.dev/github.com/cratis/arc.go)
[![Build](https://github.com/Cratis/Arc.Go/actions/workflows/build.yml/badge.svg)](https://github.com/Cratis/Arc.Go/actions/workflows/build.yml)
[![Release](https://github.com/Cratis/Arc.Go/actions/workflows/publish.yml/badge.svg)](https://github.com/Cratis/Arc.Go/actions/workflows/publish.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Discord](https://img.shields.io/discord/1182595891576717413?label=Discord&logo=discord&color=7289da)](https://discord.gg/kt4AMpV8WV)

The Go framework for [Cratis Arc](https://github.com/Cratis/Arc), bringing Arc's application-building approach to Go.

## Status

Arc.Go is **experimental v0** software. The root module has Git tags `v0.1.0`, `v0.1.1`, `v0.1.2` and `v0.1.3`. APIs may change between minor releases; while the version is v0.x, a breaking change is released as a minor version. See the [release policy](Documentation/releases.md).

Foundation packages provide artifact metadata, route resolution, UUID/temporal codecs, presence-aware JSON and result envelopes. Backend model-bound command/query pipelines add authorization, validation, preparation, nested execution, paging, rendering and interception through typed adapters, with optional guarded DI resources. HTTP hosting composes commands, snapshots, observable SSE/WebSocket queries, identity and protected discovery with explicit graceful lifecycle.

See the [compiling foundation example](example_test.go), the [minimal HTTP example](examples/hosting), the [observable query guide](Documentation/backend/go/queries/observable-queries.md) and the [feature-by-feature parity ledger](Documentation/parity.md), which records what is Implemented, Partial or unsupported.

Adapter generation (the `arc-gen` tool), the Chronicle integration and the MongoDB integration are separate Go modules inside this repository, not part of the root module. This README makes no claim about how they are installed; the [release policy](Documentation/releases.md) lists the current facts. TypeScript model/command/snapshot/observable-query [proxy generation](Documentation/backend/go/generation/typescript.md) has CLI, independent Go-consumer and locked Node runtime evidence, including a generated default WebSocket hub/Delta case, plus a bounded mounted React lane.

Browser evidence is limited. The separate [Browser observable conformance](.github/workflows/browser.yml) workflow runs the production-generated hook under the exported `<Arc>` wrapper in headless Chromium on Linux. It covers SSE hub cookie ownership, a WebSocket hub, `StrictMode`, reconnect after a server close and joined shutdown. Other browsers, cross-origin credentialed requests, suspense, paging hooks and `ReconnectPolicy` exhaustion remain unverified; the parity ledger records the exact limits. MongoDB source-only watches have bounded native/live evidence and require explicit close/join/reopen after terminal loss; Chronicle watches remain unsupported. Opaque provider-source generation, including MongoDB `Source[Find]` without explicit emission metadata, remains unsupported. See the parity ledger for the Partial provider profiles and limits.

A separate [paired snapshot HTTP checkpoint](ContractTests/httpconformance/README.md) compares 36 GET/QUERY requests in 14 groups against pinned C# Arc: 34 strict matches and two exact successful ordinary-list paging-metadata allowances. This is **Partial** coverage, not command, authentication, HEAD, provider, streaming, browser or whole-product parity.

## Installation

Requires Go **1.26 or later**. Install the root module:

```sh
go get github.com/cratis/arc.go@latest
```

To pin a tag, use for example `go get github.com/cratis/arc.go@v0.1.3`. Use the lowercase module path exactly as shown. CI checks Go 1.26 and 1.27 independently of local Go workspaces. The `tools`, `integrations/chronicle`, `integrations/mongodb` and `recipes` directories are separate Go modules in this repository; see the [release policy](Documentation/releases.md) for their current state.

## Documentation

Start with [Documentation](Documentation/index.md). API reference is on [pkg.go.dev](https://pkg.go.dev/github.com/cratis/arc.go).

## Development

From the repository root:

```sh
export GOWORK=off
go build ./...
go vet ./...
go test -race -count=1 -timeout=3m ./...
golangci-lint run
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full checks and release conventions.

## Community and security

- [Questions and help on Discord](https://discord.gg/kt4AMpV8WV) — ask questions and get help from the Cratis team and other developers
- [Documentation](https://www.cratis.io/arc/go/)
- [Cratis](https://www.cratis.io/) and the [Cratis repositories](https://github.com/Cratis)
- [Contribution guide](CONTRIBUTING.md)
- [Private vulnerability reporting](SECURITY.md)

## License

[MIT](LICENSE).
