---
title: Generate adapters and snapshot proxies
description: Generate typed Go registrations and supported TypeScript models, commands, and snapshot queries.
---

Writing a registration closure for every command repeats information already in
its method signature. The experimental `arc-gen` tool reads opted-in Go packages
and writes command/snapshot adapters for you. An optional application profile also
[generates TypeScript models, commands, and snapshot queries](typescript.md).
Observable source and
`ObservedCollection` generation require the separate tools lane to pin a fetchable
runtime revision and add its own executable evidence; use
[manual observable registration](../queries/observable-queries.md) meanwhile.
Your business methods remain ordinary Go;
the generated code uses the same public registrars and pipelines as manual wiring.

## Put behavior on the model

This declaration excerpt is from the generated consumer fixture. It assumes
`context` is imported and `State` is the application's preparation struct with a
`Name string` field:

```go
//arc:command
//arc:allow-anonymous
type Rename struct {
    Name string `json:"name"`
}

func (c Rename) Provide(context.Context) (State, error) {
    return State(c), nil
}

func (Rename) Handle(_ context.Context, state State) (string, error) {
    return state.Name, nil
}
```

Arc invokes Provide only after authorization and validation. Its exact `State`
result supplies one Handle parameter. A failed preparation never invokes Handle.
Use `commands.Preparation[State]` when preparation needs validation controls or
an explicit early stop. `Validate`-only execution invokes neither business method.

For read models, use an unnamed value receiver as a namespace. This excerpt
assumes the `Item`, `Arguments`, and `ItemQueries` application types from the
consumer fixture and the `context` import:

```go
func (Item) AllItems(ctx context.Context, args Arguments, items ItemQueries) ([]Item, error) {
    return items.All(ctx, args.Prefix)
}
```

Mark `Item` with `//arc:readmodel`. The generator discovers exported namespace
methods returning their owning model, including slices and pages. It treats the
first ordinary parameter as caller arguments and the remaining parameters as
services. Service resolution cannot turn an argument field into a dependency.

If a top-level function fits your package better, declare its owner explicitly:

```go
//arc:query model=Item name=Recent
func RecentItems(ctx context.Context, args Arguments, items ItemQueries) ([]Item, error) {
    return items.All(ctx, args.Prefix)
}
```

See the [directive and signature reference](reference.md) for authorization,
private query selection, infrastructure parameters, and rejected signatures.

## Run the tool from source

Use Go 1.26 or later. From an Arc.Go source checkout, install the tool separately
from your application's runtime dependencies:

```sh
cd tools
GOWORK=off GOTOOLCHAIN=local go install ./cmd/arc-gen
```

Keep the source revision you install pinned in your development tooling. Independent
`tools/vX.Y.Z` releases are not published by the root release workflow; their
publication waits for [Fundamentals.Go#16](https://github.com/Cratis/Fundamentals.Go/issues/16).
The tools module pins a fetchable Arc runtime revision without a local replacement.
`golang.org/x/tools` stays out of the runtime module.

From your application module, select the packages containing artifacts. For example,
if they live under `features/`:

```sh
GOWORK=off arc-gen ./features/...
GOWORK=off arc-gen -check ./features/...
```

The first command writes one `zz_arc_generated.go` per artifact package. The second
returns a nonzero exit status if regeneration would change anything. Commit generated
files with their inputs. You can put the same pinned executable command in a
`//go:generate` directive.

Select artifact packages, not a composition package that calls their generated
symbols. During analysis, old generated output is hidden so deleted or renamed
methods do not prevent regeneration. Handwritten artifact source must therefore
compile without referring to generated symbols.

## Register the package once

The generated function is `RegisterArtifacts(builder *arc.Builder, bindings ...ArcBindings) error`.
Call it from your composition package before `builder.Build()`. Check the error and
discard that builder if registration fails. No business method or service factory
runs during registration.

Without extra service parameters, pass only the builder. For dependency-bearing
methods, choose either:

- Configure the builder's `ScopeFactory` with your Fundamentals provider, then
  call `RegisterArtifacts(builder)`. The adapters declare exact dependency keys
  and resolve them through `execution.Resolve[T]` inside each execution stage.
- Supply the generated `ArcBindings` callbacks. Each callback can close over
  ordinary application objects or obtain an application resource holder from
  the supplied operation scope. No container or dependency catalog is required
  for dependencies whose callbacks you supply.

Bindings are named `ResolveReader`, `ResolveWriter`, and so on from the dependency
types; inspect the generated struct for exact names when types collide. Nil callbacks
use the default resolver and require a matching dependency catalog. Callbacks may
run concurrently and must not retain the operation scope.

Provide dependencies resolve only during preparation. Handle dependencies resolve
only after preparation is accepted. Query dependencies resolve after authorization
and argument validation. Model `Validate` methods use the existing runtime convention,
not a duplicate generated validator.

## Check a working example

The checked-in [consumer declarations](../../../../ContractTests/generatedconsumer/artifacts.go)
and [pipeline tests](../../../../ContractTests/generated_adapters_test.go) exercise
both authoring forms, plain bindings, and the Fundamentals container. From the
repository root:

```sh
go test -count=1 ./ContractTests -run TestGenerated
```

The tests execute the real pipelines and assert responses, authorization replacement,
model validation, and suppression of dependencies in denied or validate-only work.
For the underlying manual APIs, continue with
[model-bound commands](../commands/model-bound/index.md) and
[model-bound queries](../queries/model-bound/index.md).
