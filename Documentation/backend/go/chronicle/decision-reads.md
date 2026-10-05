---
title: Chronicle decision reads
description: Decide from a Chronicle read model inside a Go command and have a competing append reject the whole commit.
---

A booking command reads a room, sees it is free and appends `RoomBooked`. Between the read and the commit another command books the same room, and both succeed. An ordinary injected read model can't prevent that, because it is a snapshot: it can lag the event log and it guards nothing.

A Chronicle decision read closes that gap. It folds the instance from the event log for this command invocation and enrolls the read with the command's Chronicle transaction. When another append of an event the read model depends on, for the same key, lands before the commit, Chronicle rejects the whole batch, and Arc reports a concurrency violation instead of persisting a decision made from stale state.

This page shows the Chronicle provider. The profile, provenance and verification rules come from the root seam described in [Protected decision reads](../commands/decision-reads.md).

## Enable decisions and read in Provide

Install the integration, then certify its client as a decision provider with `sdk.EnableDecisions`. Mark the command with `commands.WithProtectedDecisions` and read through `sdk.ReadDecision` in `Provide`. The excerpt comes from the compiled `ExampleEnableDecisions` in `integrations/chronicle/sdk/decision_reads_example_test.go`, which also registers `RoomBooked`, `Room` and its projection:

```go
decisions, err := sdk.EnableDecisions(builder, adapter)
if err != nil {
    return err
}
err = commands.Register(builder.Commands(),
    commands.WithProtectedDecisions[BookRoom](),
    commands.WithoutModelValidation[BookRoom](),
    commands.WithNoResponse[BookRoom](),
    commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c BookRoom) (commands.Preparation[*sdk.Decision[Room]], error) {
        decision, err := sdk.ReadDecision(ctx, inv, decisions, rooms, readmodels.Key(c.ID))
        if err != nil {
            return commands.Preparation[*sdk.Decision[Room]]{}, err
        }
        return commands.Provided(decision), nil
    }, func(_ context.Context, _ *commands.Invocation, c BookRoom, decision *sdk.Decision[Room]) (RoomBooked, error) {
        // Arc verified the decision before Handle; a competing booking
        // of this room now rejects the whole commit.
        if room := decision.Instance(); room.Exists {
            return RoomBooked{}, validation.Reject(validation.Result{Severity: validation.Error, Message: "Room " + room.Value.ID + " is already booked."})
        }
        return RoomBooked{Guest: c.Guest}, nil
    }))
```

`*sdk.Decision[M]` implements `commands.DecisionEvidence`, so Arc verifies it after `Provide` and before `Handle`. `Instance()` returns the folded state with explicit absence. The read comes from the event log, not from the materialized projection, so a booking committed a moment ago is already visible.

`EnableDecisions` needs the integration installed on the same builder: the installed integration is the completion owner that `Build` requires for a protected command. It performs no I/O. Each call certifies a separate provider identity.

## What happens at commit

| Outcome | What you see |
| --- | --- |
| No competing append | The events commit; `Completion().Disposition` is `Committed`, or `NoPersistedWork` when the command only read. |
| Another writer appended an event the read model depends on, for the same key | Chronicle rejects the complete batch, including events for other sources. The result is `NotCommitted` with a `concurrencyViolation` validation result. Read again and resubmit. |
| The commit acknowledgement is lost | The result is `OutcomeUnknown`. Arc never retries; reconcile against the event log before resubmitting. |

Repeated reads of the same model and key share one fold and one enrollment per command frame, including reads from a command filter and from `Provide`.

## Profiles and refusals

`sdk.ReadDecision` behaves by the command's decision profile:

| Profile | Result |
| --- | --- |
| `WithProtectedDecisions` | Protected, enrolled read. |
| `WithUnprotectedDecisions` | Advisory snapshot from the ordinary read-model reader. `IsProtected()` is false and nothing is enrolled. |
| Unmarked | Refused with `commands.ErrDecisionProfile`. |

Within an active protected invocation, refusals wrap `commands.ErrDecisionRead` and preserve their original causes for `errors.Is` and `errors.As`. A nil context or invocation returns `integration.ErrInvalid` as-is.

Before acquisition (no fold or enrollment), the read is refused when:

- its arguments are invalid (`integration.ErrInvalid`) or the integration cannot resolve the command's frozen routing;
- the model is classified (`readmodels.WithPII` or any protection metadata), reducer-backed, joined, hierarchical or otherwise not admitted by Chronicle's decision catalog; `errors.As` exposes `*readmodels.DecisionReadRefused` with its reason;
- the model is not registered with the client's store (`integration.ErrNotRegistered`).

After acquisition, checking or enrollment can refuse a foreign, zero or stale token: one issued to another invocation, client, store or namespace, or invalidated by a reconnect or catalog change. These checks run again before `Handle`; the SDK also verifies token lifetime before dispatching the commit.

Custom transaction factories can refuse a non-event-log participant at enrollment with `integration.ErrUnsupported`, after the fold. The shipped `sdk.New` adapter always routes commands to the event log.

If an application ignores a failed enrollment and still returns events, the integration rolls the transaction back instead of committing unguarded work.

## Validation-only execution

`Validate` never runs `Provide`, but command filters run in validation-only execution. A decision read there folds the instance with a separate validation identity and never begins a transaction or enrolls. That read cannot be reused by a later execution of the same command.

## Limits

- A token is opaque evidence issued by the Chronicle.Go SDK, not an authenticated server proof. The pinned protocol can't atomically bind projection definitions or in-place history changes to the fold.
- The protected profile admits no model or custom validators, as described for the [root seam](../commands/decision-reads.md#limits).
- Classified models are refused rather than released: a session fold can't establish safe per-subject release.

## Verify against a kernel

`internal/integration/decisions_test.go` runs these contracts against `cratis/chronicle:19.29.4-development`: a committed booking and a stale-decision rejection, a competing dependency-event append that rejects a two-source batch, an unrelated-event append to the same source that leaves the decision valid, a lost acknowledgement that stays unknown, a separate validation-only read and the profile, classified-model and unregistered-model refusals. Run it with the commands in [Verify independently](index.md#verify-independently).
