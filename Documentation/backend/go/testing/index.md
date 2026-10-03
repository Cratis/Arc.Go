---
title: Test commands and queries without HTTP
description: Exercise the real Arc pipelines with explicit test dependencies and ordinary Go assertions.
---

Calling `Handle` directly misses authorization, validation, preparation and
resource cleanup. Use `arctest` when your specification needs those behaviors
without opening an HTTP server. It runs the application's real command and
snapshot query pipelines; it does not simulate successful effects.

## Configure the same application

Register the command or query with a normal `arc.Builder`, replacing dependencies
with explicit constructors, closures or operation resources before building.
`arctest.New(t, builder)` seals and starts that builder and registers shutdown
with `t.Cleanup`. It requires no container or assertion library.

This complete test uses a model-bound command:

```go
package greeting_test

import (
    "context"
    "testing"

    arc "github.com/cratis/arc.go"
    "github.com/cratis/arc.go/arctest"
    "github.com/cratis/arc.go/commands"
)

type Greeting struct {
    Name string `json:"name" validate:"required"`
}

func (c Greeting) Handle(context.Context) (string, error) {
    return "Hello, " + c.Name, nil
}

func TestGreeting(t *testing.T) {
    builder, err := arc.NewBuilder(arc.Options{})
    if err != nil {
        t.Fatal(err)
    }
    if err := commands.Register(builder, commands.Handle(Greeting.Handle)); err != nil {
        t.Fatal(err)
    }
    fixture := arctest.New(t, builder)
    scenario := arctest.NewCommand[Greeting, string](fixture)
    result, err := scenario.Execute(t.Context(), Greeting{Name: "Ada"})
    if got := arctest.RequireResponse(t, result, err); got != "Hello, Ada" {
        t.Fatalf("response = %q", got)
    }
}
```

Run `go test ./...`. The assertion requires both a successful result and a present
response. It still accepts legitimate zero, false and empty-string responses.
Pipeline errors remain available for `errors.Is` and `errors.As`; scenario
execution does not turn an expected failure into a fatal test assertion.

## Validate before executing

For a scenario configured as above, this excerpt checks the advisory path:

```go
result, err := scenario.Validate(t.Context(), Greeting{Name: "Ada"})
arctest.RequireNoResponse(t, result, err)
```

Validation runs authorization and validation, but not `Provide`, `Handle`,
transactional scope entry or effects. Validators may read state. To prove absence
of mutation, query the same fixture afterward or inspect your explicit dependency.

For a rejection, assert its reason rather than its message:

```go
arctest.RequireValidationReason(t, result.Details().ValidationResults, validation.DependencyUnavailable)
```

This rejection excerpt assumes `validation` is imported from
`github.com/cratis/arc.go/validation` and a result from the dependency-failure case.
`RequireValidationErrors` refuses a list containing only `dependencyUnavailable`:
a missing test dependency must not make a domain-rule specification pass. Use the
explicit reason assertion when missing dependencies are what you intend to test.

## Query the same fixture

Use `arctest.NewQuery[R](fixture, fullyQualifiedName)` to select a registered
snapshot query. `Perform(ctx, queries.Request{})` handles parameterless queries;
`queries.RequestFor(arguments, parameters)` supplies exact typed arguments.
`queries.ReadGET` or `queries.ReadQUERY` creates a request that exercises conversion
without a network request. `arctest.RequireData(t, result, err)` returns successful,
present data for ordinary Go comparisons.

A command and query scenario borrowing the same fixture share its dependencies,
not its per-operation resources. The pipeline closes each operation scope before
returning the result. Register interceptors, renderers and validators through the
builder just as in the application. The compiled examples in `arctest/example_test.go`
and task-board workflow in `ContractTests/taskboard_scenarios_test.go` exercise both
pipelines together.

## Control identity, time and lifetime

Use `execution.NewContext` to supply a trusted principal, tenant and correlation
ID for a call. This tests authorization, **not authentication of credentials**.
Configure `arc.Options.Clock` to control operation receipt time. No global clock,
principal, service provider or registry is replaced by the test kit.

Startup and test cleanup each have a five-second cooperative budget. Your
callbacks must honor their context; the helper does not abandon a goroutine to
pretend an uncooperative callback stopped. Execution uses the context you pass.
`fixture.Close(ctx)` supports an earlier explicit shutdown; later execution fails
with `arc.ErrStopped`, and test cleanup can safely call shutdown again.

If you do not have a `testing.TB`, `arctest.NewScenario(ctx, builder)` returns
`(*Scenario, error)`. You then own a bounded `Close` call. Scenario wrappers borrow
the fixture and do not own separate applications. Returned model values remain
borrowed, following the normal pipeline contract.

## Know the boundary

Command scenarios pass typed input directly, as C# `CommandScenario` does. They do
not JSON-round-trip the command or run HTTP middleware. `NewQuery` reads snapshots;
`NewObservableQuery[R](scenario, name).Capture(ctx, request, options)` synchronously
captures full results through real admission/interception/guards and owns cleanup
join. `CaptureOptions` defaults to 16 results, 1 MiB encoded retained results and a
five-second timeout. Earlier results accompany failures; reaching the count limit
succeeds only if cleanup joins. No helper claims HTTP/hub transfers, generated
adapters, Chronicle seeding, operation recovery or automatic extender discovery.
Use explicit fixture composition instead of C# service discovery and extension
policies; use returned results instead of mutable `LastResult` state.

For routing, HTTP binding, safe malformed-input responses and real process
shutdown, run the Go-owned task-board conformance gate:

```sh
go test -count=1 -timeout=2m ./ContractTests -run '^TestTaskBoardHTTPConformance$'
```

It runs the same nine selected Arc 22.14.0 cases through `httptest` and a separate
process using the real Arc host. It is not the richer HTTP, browser or streaming
parity suite. The fixture's README records the pinned source, limits and exact
coverage.

For observable framing, revisions, Full/Delta/Legacy transfers, terminal denial,
reconnect and shutdown, run the separate real-listener harness:

```sh
go test -count=1 -timeout=2m ./ContractTests/observables
```

Its independent RFC client is not a browser test. To execute the actual pinned
`@cratis/arc` transport/query APIs against the hosted Arc fixture, run:

```sh
GOWORK=off GOTOOLCHAIN=local node ContractTests/observables/frontend/run.mjs
```

This requires Node 26.8.1 and npm 12.0.2; it installs locked dependencies, compiles
manual query classes, builds the Go host and joins it after the client tests.
The observable contract README records the executed transport/mode cases and
exclusions. Node WebSocket/EventSource polyfills and class hydration are executed;
Guid/date hydration, browser cookies/origins and React-hook reconstruction remain
unverified. Delta reconstruction uses an explicitly independent test consumer,
not a claimed React hook. In-memory race/synctest cases separately prove
candidate-budget release on cancellation, explicit resource-join retries without
redisposal and retained channel cleanup after partial startup.
