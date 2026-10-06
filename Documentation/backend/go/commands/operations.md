---
title: Flat command operations in Go
description: Declare immediate inline work, inspect commit-aware recovery, and understand the manual operation boundary.
---

Returning work lets you test a command's decision without performing the write. Arc.Go executes explicitly registered operation declarations through the existing command pipeline and attempts their declared reversals only when observed persistence permits it.

This is **Partial C# parity**: manual, flat, sequential operations are supported. Generated operation adapters, receipts, nested operation workflows, and durable recovery are not implemented.

## Declare and register work

An operation implements `commands.Operation` and exactly typed `Execute(context.Context, D) error`. Optional reversal implements `Compensate(context.Context, D, commands.OperationFailure) error`. A present but incorrectly shaped `Compensate` fails registration; reflection inspects method metadata but never invokes business methods.

Register each exact pointer or value type with `commands.RegisterOperation[O, D]`. Its required `commands.Factory[D]` resolves the **complete execution and compensation dependency bundle** before any returned-value consumer or operation runs. The factory can return an application-owned service without a container, or resolve services through the originating resource scope. Optional DI keys receive the existing static catalog checks without activation. Hidden closure dependencies cannot be checked statically.

Business inputs belong in declaration fields; services belong in method parameters. Batch membership is copied, but payloads remain borrowed, including pointer payloads. Do not mutate them during execution. Factories and borrowed services must support concurrent commands. Never retain preflight `Scope`, `Invocation`, or resolver views as execution dependencies. Existing resource owners, not operation declarations, dispose services.

The following excerpt assumes the application's `Reservations` interface provides `Reserve` and ownership-aware `CancelOwned` methods:

```go
type ReserveSeat struct{ ReservationID, SeatID string }

func (ReserveSeat) CommandOperation() {}
func (o ReserveSeat) Execute(ctx context.Context, service Reservations) error {
    return service.Reserve(ctx, o.ReservationID, o.SeatID)
}
func (o ReserveSeat) Compensate(
    ctx context.Context, service Reservations, _ commands.OperationFailure,
) error {
    return service.CancelOwned(ctx, o.ReservationID)
}
```

`commands/operation_example_test.go` contains the complete external plain-Go example: model-bound `Handle`, manual registration, and actual typed pipeline execution. Run it with `go test ./commands -run ExampleRegisterOperation`. Calling `Handle` directly only declares work.

## Return shapes

| Declared return | Meaning |
| --- | --- |
| Concrete operation or `commands.Operation` | One server-only invocation; nil singular values are absent |
| `commands.Operations` | Explicit ordered batch; zero and empty batches contain no work |
| `commands.Outcome[R]` with `commands.WithOperations[C]()` | Explicit composition of operations, controls, other server effects, and at most one response |
| Raw operation slice or array | Rejected; use `commands.NewOperations` |
| Raw `any` containing an operation | Rejected; retain meaningful static participation |
| Ordinary DTOs or `[]any` | Ordinary response classification; Arc does not recursively search them for operations |

`commands.NewOperations` copies membership and rejects nil members, including typed nils. Operation leaves are reserved for Arc: broad response handlers and context updaters never receive them. An operation cannot occupy the explicit response position in `commands.Respond`. Explicit responses retain scalar-zero presence on success; every failed result retracts its response.

`WithOperations` is required for erased `Outcome` effects, even if a particular invocation returns no operations. Direct operation and batch signatures infer participation. Empty and absent returns still require a compatible execution boundary. `Validate` runs authorization and input validation only: no operation dependencies, execution participants, preparation, handling, or recovery.

## Execution and scope compatibility

Arc validates every declaration, resolves every dependency bundle, processes controls using the command severity policy, consumes other returned effects, then executes operations sequentially. A failed control or consumer prevents operation entry. A journal entry is recorded inside the admitted callback immediately before the business call, after cancellation and security checks. A method returning nil is recorded as completed even if post-call cancellation or ignored nesting subsequently rejects the command.

Every scope must opt in statically:

- `Registry.AddOperationExecutionScope` promises noncommitting `Begin` and `Complete`, with recovery dependencies usable until resource disposal.
- `Registry.AddOperationCommitParticipant` occupies the existing sole terminal slot. Its `ObserveCommit` hook reports provider facts before external operation entry; `Complete` supplies the authoritative final report.
- Unclassified ordinary or terminal scopes reject operation commands at `Build`, before factories or `Begin`. They remain supported for non-operation commands.
- The installed Chronicle integration registers through `AddOperationCommitParticipant` and hosts operation commands. See [Chronicle compensation](../chronicle/index.md#compensate-operations-against-chronicle-outcomes) for append observer/resolver prerequisites and remaining limitations.

Ordinary scopes complete in reverse entry order, then the sole terminal participant completes. Only afterward does Arc choose recovery, while the originating resource owner is still alive. Pending pre-entry `NotCommitted` observations are not merged as finalized attempts; confirmed, unknown, or mixed persistence facts remain sticky and prohibit operation entry.

Same-host nested commands are refused whenever parent or child is operation-capable, before child factories. This includes advisory validation and calls using the original pipeline with the callback context. Ignored refusals reject forward execution; a new refusal during a compensator is recorded as compensation failure, without preventing earlier eligible compensators. `Execution.CheckExplicitCommit` provides the same sticky early-commit guard for future provider integrations; it does not itself commit anything.

## Recovery and observations

For successful A and B, partially failing C, and never-started D, eligible compensation runs **C, B, A**, never D. Missing reversal is observable incomplete recovery.

| Final `Result.Completion().Disposition` on failure | Recovery |
| --- | --- |
| `NoPersistedWork` or `NotCommitted` | Reverse declared compensators |
| `Committed` | Suppressed; no automatic reversal |
| `OutcomeUnknown` or `MixedCommit` | Indeterminate; no automatic reversal |

The first failure and response-free result are frozen before later completion or recovery observations. `OperationFailure` supplies invocation index, successful method return, failing-invocation identity, original phase, final `CompletionReport`, original result and original error. Compensation failures never replace the original error or enter HTTP exception messages. Existing completion and disposal error wrapping remains unchanged.

`Result.Recovery()` returns a value snapshot when operation processing participated. `Result.OperationOutcomes()` returns copied observations with operation type, execution completion and compensation outcome, never declaration payloads. Both are backend-only; neither changes HTTP JSON or the TypeScript result envelope. `Completion()` remains the sole persistence report.

Configure `commands.PipelineOptions.Operations` or root `arc.Options.CommandOperations` with `commands.OperationOptions`. Zero `CompensationTimeout` selects **30 seconds**; negative values fail construction. One fresh detached budget begins after terminal completion, preserving principal and tenant presence, correlation, receipt, command metadata and resource ownership. Callbacks run synchronously and remain joined even if they ignore cancellation. After expiry no further compensator starts, and no timed-out callback is restarted. Pending resource cleanup may resume joining only, never operation execution or compensation.

Completed recovery means callbacks returned, not that external history was erased or a retry is safe. Providers own partial-application, lost-acknowledgment, attempt ownership and tenant isolation guarantees. There is no automatic retry, distributed atomicity, durable journal, or crash recovery. Post-pipeline serialization, delivery and disposal failures do not initiate another recovery attempt.

## Parity evidence

The authority is C# Arc revision `7c1e78075b737df64f69fddfaae83374f75e3612`: `Arc.Core/Commands/CommandOperationExecution.cs`, `CommandOperationBoundary.cs`, the operation execution/recovery reference, and `Arc.Core.Specs/Commands/for_CommandOperationExecution`.

Go evidence is in `commands/operations_test.go`, `operation_execution_test.go`, `operation_boundaries_test.go`, and `operation_example_test.go`: declaration copying and metadata, complete preflight, all five existing dispositions, partial failure, controls, denial and validation nonactivation, sticky nesting, cancellation, panic, detached budgets, resource retention/join resumption, error identity and actual HTTP privacy. These core fixtures alone do not establish Chronicle provider compatibility; the [Chronicle integration guide](../chronicle/index.md#compensate-operations-against-chronicle-outcomes) describes its separate provider and kernel evidence.
