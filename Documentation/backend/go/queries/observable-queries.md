---
title: Keep a query live
description: Register an owned source, publish current values, and consume observable query results in Go.
---

Polling a list repeatedly wastes requests and still leaves your screen behind the
latest state. An observable query keeps one admitted subscription open and sends
new results as your application publishes them. You do not need Chronicle or a
container. This is an experimental Go API; generated observable adapters and
actual JavaScript/browser compatibility remain separate, unverified boundaries.

## Register an application-owned feed

Use `queries.RegisterObservable[M,A,O]` with a performer returning
`observable.Source[O]`. `M` owns the query; `O` is the emitted value, not the
source itself. Type inference supplies `A` and `O` when you specify only `M`.

This registration excerpt assumes a read-model struct `Task` with `ID` and
`Title` fields, an existing `*arc.Builder` named `builder`, and the `context`,
`metadata`, `observable` and `queries` imports from Arc.Go:

```go
state, err := observable.NewState([]Task{}, observable.SubjectOptions[[]Task]{Buffer: 16})
if err != nil {
    return err
}
err = queries.RegisterObservable[Task](builder, "All", queries.Function(
    func(context.Context, queries.NoArguments) (observable.Source[[]Task], error) {
        return state, nil
    },
), queries.WithPath[queries.NoArguments]("/tasks/all"),
    queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true}))
if err != nil {
    return err
}
```

The initial empty slice is a ready result with `data: []`. Use
`NewPendingState` when no first value exists yet. The performer runs after
admission, once per subscription. You can delegate that closure to an unnamed
value-receiver namespace method just as with [snapshot queries](index.md).
`AllowAnonymous` is explicit example policy, not a tenancy or row-access grant.

Publish through `state.Publish(ctx, tasks)` and check the returned error. Each
subscriber gets an independently owned stream; disconnecting one subscriber does
not complete the application-owned state. `Complete` ends the source normally;
`Fail` ends it with an error. A real nil or zero value is an emission, not EOF.

## Choose the source lifetime

| Source | Use it when |
| --- | --- |
| `NewState(initial, options)` | You already have a current value; Open atomically attaches and replays it |
| `NewPendingState(options)` | The first value will arrive later |
| `NewSubject(options)` | You need emissions without a retained current value |
| `FromProducer` | You have a cancellation-aware producer callback |
| `FromIterator` | You have an iterator with an owned early-stop path |
| `FromChannelFactory` | You can supply a channel and explicit producer cleanup/join |

Construction starts no I/O or goroutine. An opened stream is single-consumer;
`Next(ctx)` returns `io.EOF` only for normal completion. `Close(ctx)` cancels and
joins the subscription. A timed-out close remains owned and needs a later join.
Bare borrowed channels are not sufficient lifecycle-bearing query declarations.
If a channel factory returns cleanup alongside a startup error or invalid channel,
Open still returns an owned stream with that error. Close the nonnil handle using
a separate cleanup budget; cancellation alone never joins its producer. The query
pipeline retains incomplete failed-opening cleanup for later Shutdown, including
when cleanup panics. `observable.ErrJoinPending` means completion is unknown or
incomplete; retain the handle and its dependencies. A channel factory's cleanup
callback must allow serialized continuation after a context error, this sentinel
or a panic. Initiate cancellation/disposal side effects at most once inside the
callback; subsequent attempts only finish joining the same producer. Any other
returned outcome certifies completion, including failure, and is cached rather
than retried. A recovered panic remains inspectable as `execution.PanicError`
after a successful later join, but no longer carries `ErrJoinPending`.
`WithEnumerable[A]()` marks a streaming-only source, including the reference's
direct null-skipping behavior; ordinary subject nils remain ready emissions.

Published slices, maps and pointed-to values must remain immutable. Set
`SubjectOptions.Clone` when your application cannot transfer an immutable value;
a shallow slice copy is insufficient for mutable nested fields. The pipeline
detaches each candidate before rendering and interception, but that cannot make
concurrent publisher mutation safe.

Subject overflow terminates only the slow subscriber. A publish may reach some
subscribers and fail for others; inspect `observable.PublishError` and do not
blindly retry a partially successful publish. There is no silent drop-oldest
policy or automatic coalescing.

## Consume from Go

`queries.Perform[R]` reads an observable current/pending snapshot. Add
`Request.WithWait(queries.WaitOptions{ForFirstResult: true})` to wait for the
first allowed result. [HTTP snapshot and wait rules](observable-http.md) use the
same pipeline.

`queries.Subscribe[R](ctx, pipeline, name, request, callback)` synchronously owns
open, full-result delivery and close. The callback returns nil only when your
consumer accepts delivery; an error stops consumption. Cancellation stops the
observation and joins its cleanup. No caller-created goroutine is required.

For explicit ownership, obtain the additive `queries.ObservablePipeline`
capability, call `Open`, and then `Observation.Run`. Check both the admission
result and local error; a nil observation is not successful activation. Run is
single-use. You must call `Close` with a bounded cleanup context, never from the
delivery callback. A custom snapshot-only pipeline returns
`ErrObservableCapability`; observable `PerformScoped` is unsupported rather than
borrowing a command's shorter lifetime.

## Stop safely and isolate tenants

Resolve the tenant's authorized feed after admission. A global unfiltered
subject plus field masking is not row authorization and can leak counts.
Every candidate rechecks membership and authorization, then runs interception
and [emission guards](observable-emission-guards.md).

`Application.Shutdown(ctx)` stops admission and cancels/joins observations and
hub connections before draining unary work or stopping user hooks. If work does
not join within the budget, the application remains Stopping; a later Shutdown
continues cleanup. Do not dispose borrowed collaborators after a timeout.

Next, [inspect a query over HTTP](observable-http.md) or choose
[hub transfers](observable-query-hub.md) for multiplexing and collection deltas.
