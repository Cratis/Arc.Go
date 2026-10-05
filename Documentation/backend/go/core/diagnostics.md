---
title: Backend operation diagnostics
description: Record bounded, payload-free command and query outcomes and export them on your own schedule.
---

When a command starts failing validation or a query slows down, you need counts and
durations per artifact, not request logs full of payloads. Arc.Go's optional
`observability.Recorder` counts each command and query outcome by registered
artifact name, keeps a bounded ring of recent observations, and never calls your
code while a request runs. You decide when and where to export.

This translates the outcomes of C# Arc's `Observability/PipelineMetrics.cs` and
`Observability/OperationOutcomes.cs` (Arc `7c1e780`). Status: **Partial**.
Cumulative counts and duration sums replace .NET meter histograms. There is no
tracing provider, no histogram buckets and no built-in exporter.

## Record a pipeline

`ExampleRecorder` in `observability/example_test.go` is the complete compiled
example. It builds a command pipeline with a recorder, executes one command and
drains the result:

```go
recorder, err := observability.NewRecorder(observability.Options{})
if err != nil {
    panic(err)
}
var registry commands.Registry
if err := commands.Register(&registry, commands.Void(func(announce, context.Context) error { return nil })); err != nil {
    panic(err)
}
pipeline, err := registry.Build(commands.PipelineOptions{Diagnostics: recorder})
if err != nil {
    panic(err)
}
result, err := pipeline.Execute(context.Background(), announce{Message: "not in diagnostics"})
if err != nil {
    panic(err)
}
fmt.Println(result.IsSuccess())
// Export explicitly, after execution. No callback runs inside the pipeline.
report := recorder.Drain(64, func(event observability.Observation) error {
    fmt.Println(event.Outcome.String())
    return nil
})
fmt.Println(report.Removed)
```

It prints `true`, `success` and `1`. Run it with:

```bash
go test -run ExampleRecorder ./observability
```

For a hosted application, set `arc.Options.Diagnostics` instead. The builder passes
the same recorder to its command and query pipelines, and HTTP ingress records
matched requests that fail before dispatch: credential failures, command body
decode failures and query reader failures. Query pipelines take
`queries.PipelineOptions.Diagnostics` when you build them yourself.

## What an observation contains

An `Observation` holds copied scalars only: artifact label, operation, transport,
phase, outcome and elapsed seconds. It cannot carry a payload, an argument, a
principal or an error value.

| Field | Values |
| --- | --- |
| `Operation` | `Command`, `Validate`, `Query` |
| `Transport` | `Unknown` (commands), `SnapshotTransport`, `ObservableTransport` |
| `Phase` | `Completed`, `Opening`, `Consumption`, `FirstDelivery`, `Delivered`, `Joined`, `Cleanup` |
| `Outcome` | `success`, `validation`, `authorization`, `append_rejected`, `cancelled`, `error` |

Authorization takes precedence over other failures. `cancelled` means a failed
operation whose context was canceled. `append_rejected` maps constraint and
concurrency validation reasons.

Observable queries record separate phases. `Opening` covers admission and source
activation; `Consumption` covers one consumer's terminal outcome; `FirstDelivery`
and `Delivered` count acknowledged data results; `Joined` and `Cleanup` measure
time until source and resource ownership is actually released. Suppressed,
denied, obsolete or failed deliveries do not count as delivered. A cleanup that
times out or panics stays retained until its owner releases it.

## Bounds and labels

| `observability.Options` field | Default | Maximum |
| --- | --- | --- |
| `ArtifactLimit` | 1000 registered labels | 1000 |
| `EventCapacity` | 256 queued observations | 65536 |

Pipelines register their frozen command and query identities when they build.
Unknown names and names past the limit are labeled `_other`
(`observability.Other`), so request input can never grow the label set. Names
longer than 512 bytes also use `_other`. One recorder can be shared by several
pipelines; the limits are recorder-wide and earlier registrations win.

Metrics are cumulative and never dropped. The event ring is separate: when it is
full, new events are dropped and `Snapshot().Dropped` counts them. `Delivered`
phases update metrics only, so a busy stream does not fill the ring.

## Export on your schedule

- `Snapshot()` copies the metrics in stable label order, the queued events and the
  dropped/export-failure counters. It never calls an exporter.
- `Drain(limit, observe)` removes up to `limit` events, releases the lock, then
  calls `observe` synchronously for each. Errors and panics are counted in
  `ExportErrors` and `ExportPanics` without retaining their values. Removed events
  are never retried.

A blocked exporter blocks only the goroutine that called `Drain`. The recorder
starts no goroutines and has no timeout; own cancellation and export I/O in your
application. A nil `*Recorder` and the zero value are disabled.

## Inspect query health

To see how many observable queries are open, closing or still retained, enable
the protected [query health endpoint](../queries/query-health.md). It shares the
same recorder for its own diagnostics.

## Evidence

`observability/recorder_test.go`, `commands/diagnostics_test.go`,
`queries/diagnostics_test.go`, `queries/diagnostics_observation_test.go`,
`queries/diagnostics_retained_test.go`, root `diagnostics_test.go` and
`diagnostics_http_test.go` cover the bounds, outcomes, observable phases, retained
cleanup, HTTP pre-dispatch failures and the absence of payload values. See the
[parity map](../../../parity.md) for the full record.
