---
title: Protected decision reads
description: Opt a command into invocation-owned, enrolled read-model decisions and supply them through the provider-neutral seam.
---

A command that decides from a read model can lose a race: another command changes the same instance between the read and the write. A protected decision read prevents that. The read belongs to one command invocation and is enrolled with the provider's completion owner, so a competing write rejects the whole batch instead of committing a decision made from stale data.

The `commands` package owns the profile, the cache and the provenance checks. A provider integration such as Chronicle supplies the evidence, and you never handle provider tokens directly. The root package imports no provider.

## Choose a profile

Each command has one frozen `DecisionProfile`, readable through `Registration.DecisionProfile()`:

| Profile | Option | C# counterpart | Decision reads |
| --- | --- | --- | --- |
| `DecisionsUnmarked` (default) | none | unmarked command | Refused. Ordinary read-model injection stays advisory. |
| `DecisionsProtected` | `WithProtectedDecisions[C]()` | `[ProtectedDecision]` | Invocation-owned, enrolled and verified before `Handle`. |
| `DecisionsUnprotected` | `WithUnprotectedDecisions[C]()` | `[Unprotected]` | Refused by Arc; the provider serves an advisory, detached snapshot. |

The two options share one slot, so supplying both fails with `ErrDuplicate`.

## Admission happens before any callback

The protected profile can't certify arbitrary validation code, so it's admitted only for commands that run none:

- `Register` refuses the profile unless `WithoutModelValidation` is supplied. It also refuses it with any `WithValidator` or `WithScopedValidator` option, or with command operations. The error is a `*RegistrationError` that matches `ErrDecisionProfile` and `ErrInvalidRegistration`.
- `Build` refuses a protected command unless the registry has a certified provider (`Registry.AddDecisionProvider`) and a completion owner (`AddDeferredCommitParticipant`). A failed `Build` leaves the registry editable.
- At run time, `ReadDecision` repeats these checks and refuses an unmarked or unprotected command, an uncertified provider, or a missing owner before calling any provider stage.

Provider certification is composition, not a runtime flag. A `DecisionProvider` from `NewDecisionProvider()` is an opaque identity; a read whose target names a provider that this registry never added is refused.

## Read and provide a decision

This excerpt comes from the compiled `ExampleWithProtectedDecisions` in `commands/decision_example_test.go`, which also declares `BookRoom`, `Room`, the `roomSource` decision source and the registry composition.

```go
err := commands.Register(&registry,
    commands.WithProtectedDecisions[BookRoom](),
    commands.WithoutModelValidation[BookRoom](),
    commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c BookRoom) (commands.Preparation[*commands.DecisionRead], error) {
        target := commands.DecisionTarget{Provider: provider, Model: reflect.TypeFor[Room](), Store: "hotel", Namespace: "default", Key: c.Room}
        read, err := commands.ReadDecision(ctx, inv, target, roomSource{})
        if err != nil {
            return commands.Preparation[*commands.DecisionRead]{}, err
        }
        return commands.Provided(read), nil
    }, func(_ context.Context, _ *commands.Invocation, c BookRoom, read *commands.DecisionRead) (commands.NoResponse, error) {
        // Arc verified read before Handle; decide from it.
        fmt.Println("booking", c.Room, "booked:", read.Value().(*Room).Booked)
        return commands.NoResponse{}, nil
    }))
```

In practice the provider integration wraps `ReadDecision` in a typed API, and its typed value implements `DecisionEvidence`.

`ReadDecision` shares one acquisition per command frame and target. Concurrent callers wait for the same fold; the first caller's context owns it, and a canceled waiter doesn't cancel it. A failed acquisition is cached for the frame and never retried. Every call checks the evidence again and enrolls it again with the current owner. Every target field (provider, model type, store, namespace and key) is part of the cache key.

Nested commands and validation-only runs get their own cache. A `Validate` run never enrolls and doesn't need an owner.

## Foreign, nil and expired reads are refused before effects

After `Provide` and before `Handle`, Arc verifies every read carried by a payload that implements `DecisionEvidence` (`*DecisionRead` does). It refuses:

- a nil or zero `DecisionRead`, or, for a protected command, evidence that carries no reads;
- a `DecisionRead` whose evidence was replaced after issue, for example by copying another read over it;
- a read issued to another invocation, another command, a validation-only run or another pipeline;
- a read the provider's `Check` no longer accepts;
- a read used after its callback expired, or under a changed principal or correlation.

This applies to every profile: an unmarked or unprotected command has no issued reads, so any read it carries is refused. Such a command may still return a provider-typed `DecisionEvidence` value that carries no reads, which is how a provider serves an advisory snapshot. Call `VerifyDecision` yourself before relying on a read in a callback Arc doesn't verify. Arc only inspects payloads that implement `DecisionEvidence`; it doesn't look inside other structs or slices.

Every refusal wraps `ErrDecisionRead` and keeps the provider's cause for `errors.Is` and `errors.As`. It is an infrastructure failure, never a validation finding, so `ExecuteOptions.AllowedSeverity` can't turn it into success.

## Implement a provider

A provider integration implements `DecisionSource` for each target:

| Stage | Contract |
| --- | --- |
| `Admit` | Refuse unsupported models, such as classified ones, before any acquisition. |
| `Acquire` | Return opaque, non-nil provider evidence. Never accept an application-created token. |
| `Check` | Validate the evidence's lifetime and target again; called at every issue and verification. |
| `Enroll` | Register the evidence with the provider's current completion owner. Not called in validation-only runs. |

Arc checks callback and security continuity between stages, holds no lock while calling them, and converts a panic to an error. Use `CurrentDecisionProfile` to serve advisory snapshots for unprotected commands and to refuse decision reads for unmarked ones.

## Limits

- The protected profile admits no model validation or custom validators. C# Arc certifies parameterless validators that Arc constructs itself; Go has no equivalent certification yet.
- Arc doesn't detect decision reads delivered through other dependency paths; only `Provide` payloads and explicit `VerifyDecision` calls are checked.
- Competing-write rejection, lost-acknowledgement handling and release semantics belong to the provider and its completion owner. The protocol gives no authenticated server proof that a read was enrolled.
