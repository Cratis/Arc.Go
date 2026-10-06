---
title: Command execution scopes
description: Enlist root completion participants and safely share command resources with nested execution.
---

Select a response only after the command boundary has finished. An `ExecutionScope` can still reject completion, so the pipeline publishes no failed response even when Handle returned a value.

## Participate in root completion

Register participants with `AddExecutionScope(name, factory, keys...)`. The pipeline lazily constructs borrowed participants during root Execute, before membership and declaration authorization. This early phase is trusted extension work, not a place to eagerly construct protected handler dependencies.

`Begin(ctx, invocation)` runs in registration order. `Complete(ctx, invocation, result)` runs in reverse order for every entered Begin, including the participant whose Begin failed. A failure does not skip earlier participants' completion. Later, unentered participants are not completed.

Completion receives the already-failed result after cancellation and a live, bounded cleanup context. Its result fragment can only add verdicts and diagnostics; it cannot restore a failed response. Only owned resource disposal follows completion. Validate never constructs participants.

## Distinguish ownership from resources

An `execution.Scope` guards security and resource lifetime. A `commands.Execution` guards command owner/frame admission. Neither is a transaction, authorization token, or observed commit fact.

The unbound pipeline starts a fresh owner and independent resources even when called from inside Handle. `Invocation.Pipeline()` instead joins the active owner and borrows compatible resources. Participants begin and complete once at the root, not for each child.

Child commands get distinct contexts and receipts while preserving correlation. A bound executor rejects a changed correlation; use an independent root execution for a new operation. Ignored child failures remain sticky: discarding a result does not turn the root into success. A child uses its own bound executor for further nesting; reusing an active ancestor executor fails.

## Keep nested calls synchronous

The bound executor rejects concurrent admission, expired callbacks, security mismatch, and Execute from a validation-only frame. Do not retain it or leave child work running after the callback returns. The framework joins an admitted child before expiring that callback and reports invalid concurrent lifetime use; it cannot forcibly stop an application callback that ignores cancellation.

Queries sharing an explicitly supplied operation scope do not enlist in command completion. These generic participants do not implement Chronicle transactions, operations, compensation, or recovery. Cleanup is not rollback.

## Complete deferred persistence last

`AddDeferredCommitParticipant` registers one provider-neutral terminal participant. Its Begin runs before ordinary participants; its Complete runs after all ordinary completion callbacks, regardless of registration order. The result includes ignored nested failures and cancellation. The participant must not commit open work when that result is unsuccessful. New nested commands cannot enter during terminal completion. A second terminal participant is rejected: this is not distributed transaction coordination.

Complete returns a `CompletionReport` and an error. Report `NoPersistedWork`, `NotCommitted`, `Committed`, `OutcomeUnknown`, or `MixedCommit` from provider evidence, not merely a nil error or a completed flag. Domain rejection belongs in a validation-bearing error; input severity filtering does not suppress terminal failures. Begin reserves ownership without connection or protected dependency activation. Validate never activates this participant.

Before explicit early persistence, `Execution.CheckRecordedFailures(ctx)` checks this frame and its ancestors for already-recorded failures, including ignored nested Execute outcomes. Advisory nested Validate remains advisory. This guard grants no authorization and cannot predict later failures.

`commands.ReportCommit` records an early completion. `Result.Completion()` retains the report through later command, resource-disposal, or serialization failures; `CompletionError` also retains the cause for `errors.Is` and `errors.As`. Neither the report nor disposition adds an HTTP envelope field. Failed commands still omit every response, including false, zero, and empty strings. Unknown outcomes require reconciliation or application idempotency before resubmission, not an automatic retry.

Resource disposal follows persistence and can fail after a confirmed commit. Returning a failed command in that situation does not undo the write.
