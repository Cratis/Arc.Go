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

Child commands get distinct contexts and receipts while inheriting correlation unless explicitly changed. Ignored child failures remain sticky: discarding a result does not turn the root into success. A child uses its own bound executor for further nesting; reusing an active ancestor executor fails.

## Keep nested calls synchronous

The bound executor rejects concurrent admission, expired callbacks, security mismatch, and Execute from a validation-only frame. Do not retain it or leave child work running after the callback returns. The framework joins an admitted child before expiring that callback and reports invalid concurrent lifetime use; it cannot forcibly stop an application callback that ignores cancellation.

Queries sharing an explicitly supplied operation scope do not enlist in command completion. Chronicle transactions, operations, compensation, commit facts, and recovery are not implemented by these generic participants. Cleanup is not rollback.
