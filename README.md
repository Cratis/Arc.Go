# Arc for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/cratis/arc.go.svg)](https://pkg.go.dev/github.com/cratis/arc.go)
[![Build](https://github.com/Cratis/Arc.Go/actions/workflows/build.yml/badge.svg)](https://github.com/Cratis/Arc.Go/actions/workflows/build.yml)
[![Release](https://github.com/Cratis/Arc.Go/actions/workflows/publish.yml/badge.svg)](https://github.com/Cratis/Arc.Go/actions/workflows/publish.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

The Go framework for [Cratis Arc](https://github.com/Cratis/Arc), bringing Arc's application-building approach to Go.

## Status

**Early development.** Foundation packages provide artifact metadata, route resolution, UUID/temporal codecs, presence-aware JSON and result envelopes. Backend model-bound command/query pipelines now add authorization, validation, preparation, nested execution, paging, rendering and interception through typed adapters, with optional guarded DI resources. Releases will remain **v0.x** while the API is experimental. There is no tagged Go release yet; the installation command and Go reference will become usable after the first release.

See the [compiling foundation example](https://github.com/Cratis/Arc.Go/blob/main/example_test.go) and [feature-by-feature parity ledger](https://github.com/Cratis/Arc.Go/blob/main/Documentation/parity.md). HTTP hosting composes commands, snapshots, observable SSE/WebSocket queries, identity and protected discovery with explicit graceful lifecycle. Try the [minimal HTTP example](https://github.com/Cratis/Arc.Go/tree/develop/examples/hosting) and [observable query guide](https://github.com/Cratis/Arc.Go/blob/develop/Documentation/backend/go/queries/observable-queries.md). Adapter generation, TypeScript model/command/snapshot/observable-query proxies, and optional Chronicle/MongoDB integration are experimental. [Proxy generation](https://github.com/Cratis/Arc.Go/blob/develop/Documentation/backend/go/generation/typescript.md) has actual CLI, independent Go-consumer, and locked Node runtime evidence, including a generated default WebSocket hub/Delta case. The wider manual transport lane remains separate. Final delta collections use an independent test-consumer reducer, not mounted React; browser/React-hook conformance remains unverified. Opaque provider-source generation, including MongoDB `Source[Find]` without explicit emission metadata, and database watches remain unsupported.

## Installation

Requires Go **1.26 or later**. Once a version has been published:

```sh
go get github.com/cratis/arc.go@latest
```

Use the lowercase module path exactly as shown. CI checks Go 1.26 and 1.27 independently of local Go workspaces.

## Documentation

Start with [Documentation](Documentation/index.md). API reference will be available on [pkg.go.dev](https://pkg.go.dev/github.com/cratis/arc.go) after publication.

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

- [Cratis](https://www.cratis.io/) and the [Cratis repositories](https://github.com/Cratis)
- [Contribution guide](CONTRIBUTING.md)
- [Private vulnerability reporting](SECURITY.md)

## License

[MIT](LICENSE).
