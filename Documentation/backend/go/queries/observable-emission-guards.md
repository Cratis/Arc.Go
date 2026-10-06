---
title: Guard each observable emission
description: Suppress candidates or terminate subscriptions after authorization and detached interception.
---

Admission alone cannot authorize everything a long-lived query will publish.
Membership may change while a connection stays open. Arc rechecks membership and
query declarations for every candidate; emission guards add your subscription-
specific delivery policy after rendering and interception. They are not a
replacement for authentication, tenant isolation or row selection.

## Register a lazy guard

`builder.Queries().AddEmissionGuard(name, factory, keys...)` appends a uniquely
named guard factory. Factories receive `context.Context` and `*execution.Scope`;
optional exact dependency keys are checked at Build without activation. Factories
activate only for authorized observable candidates, not ordinary unary queries.

This composition excerpt assumes `builder` is an `*arc.Builder`, `context`,
`execution` and `queries` are imported, and `mayReceive(ctx, c)` is your
concurrent-safe policy function returning `(bool, error)`:

```go
err := builder.Queries().AddEmissionGuard("subscription-access",
    func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
        return queries.EmissionGuardFunc(func(ctx context.Context, c queries.EmissionContext) (queries.EmissionVerdict, error) {
            allowed, err := mayReceive(ctx, c)
            if err != nil {
                return queries.DenyAndTerminate, err
            }
            if !allowed {
                return queries.DenyAndTerminate, nil
            }
            return queries.Allow, nil
        }), nil
    })
if err != nil {
    return err
}
```

The policy runs under subscriber metadata, not the publisher's identity/tenant.
A callback error, panic or unknown verdict fails closed; raw errors stay local.

## Choose a verdict

| Verdict | Effect |
| --- | --- |
| `Allow` | Continue to later guards and delivery |
| `Suppress` | Skip this candidate, still run later guards that might deny |
| `DenyAndTerminate` | End this observation with an unauthorized result |

Guards run in registration order. Deny wins immediately. Suppression does not
satisfy first-result waits, advance the first-delivered flag or commit a delta
baseline. A later allowed candidate compares with the last delivered value.
Direct streams send a terminal unauthorized QueryResult; hubs send Unauthorized
for only the current subscription owner. No later data follows its terminal frame.

## Capture only subscription data

`EmissionContext` exposes `Name`, `Arguments`, `Principal`, `Tenant`,
`CorrelationID`, `QueryContext`, `FirstDelivered` and `SubscriptionScope`.
`FirstDelivered()` means **no candidate has yet been delivered**, not that the
current callback already succeeded. QueryContext contains the subscription's
receipt and stable correlation.

An input filter can call `Invocation.SetSubscriptionScope(value)` before performer
activation. The pipeline freezes it after filtering; input filters are not rerun
on every emission. Arguments and scope data are reconstructed independently for
each guard. Mutating one guard's snapshot cannot change later guards or emissions.

Scope data is JSON-round-trippable data, never a dependency resolver or resource
lease. Streams, functions, channels, cycles and unrepresentable private state are
rejected; nesting is bounded. Do not put request headers, bodies or a subscribe
POST's disposable resources into it. Use the owned execution scope for declared
services and ensure collaborators remain available until the observation joins.

## Preserve privacy before transfer

The candidate order is authorization → detached rendering → read-model
interception → guards → encoding/transfer → acknowledged delivery. This uniform
path applies to current snapshots, waits, subject and enumerable emissions, direct
transports and both hubs; it deliberately closes reference-path gaps.

Select authorized rows before counting/paging. Field masking cannot protect
`totalItems`. Delta removals contain previously delivered intercepted items,
never a newly resolved unmasked provider value. See
[read-model interception](read-model-interception.md) and
[change-stream baselines](change-stream.md).
