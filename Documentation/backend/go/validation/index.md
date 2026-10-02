---
title: Validation findings
description: Invoke typed validators and apply bounded command severity allowances without weakening authorization.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Use `validation.Result` for feedback callers can act on, not exception messages.
The foundation supplies typed invocation and severity policy as well as result
contracts. Automatic validator discovery, graph traversal and command/query
pipeline integration remain deferred.

## Fields

| Field | Contract |
| --- | --- |
| `Severity` | Numeric `Unknown` 0, `Information` 1, `Warning` 2, `Error` 3 |
| `Message` | Safe client-visible feedback |
| `Members` | Wire member paths, including indexed nested paths; nil emits `[]` |
| `State` | Optional application data; nil and typed nil are omitted |
| `Reason` | Open string category; an empty Go value emits `rule` |
| `ReasonDetail` | Optional specific identity, such as a constraint name |

Known reasons are `rule`, `concurrencyViolation`, `constraintViolation`,
`validatorFailed`, `dependencyUnavailable` and `malformedRequest`. Unknown reasons
are preserved so a newer server does not break older clients.

A retained finding of **any severity** makes command/query `IsValid()` false.
Apply command policy before constructing the final result; serialization never
silently removes warnings or information. Queries initially retain all findings
and do not consume command allowance headers.

Messages and state are not automatically redacted. Do not put credentials, parser
internals or other sensitive details in these fields. `Clone()` copies members and
reason-detail storage, but continues to borrow application `State`.

## Invoke typed validators

Implement `Validator[T]` or pass a plain `ValidatorFunc[T]` closure. This excerpt
assumes imports for `validation` and `context`:

```go
validator := validation.ValidatorFunc[int](
    func(_ context.Context, value int) ([]validation.Result, error) {
        if value < 0 {
            return []validation.Result{{Severity: validation.Error, Message: "Must be positive."}}, nil
        }
        return nil, nil
    })
```

Call `validation.Invoke(ctx, validator, value)`. Validators run synchronously,
borrow input and must honor cancellation. Invoke clones findings, retaining borrowed
State. `Reject(results...)` copies findings into a `Failure`; an empty rejection
is nil. Wrapped Failure errors remain recognizable through `errors.As` and
`errors.Is(err, ErrRejected)`.

Unexpected errors, panics and unsupported callback severities become an
`InvocationError` implementing Failure. Its one finding is Error with reason
`validatorFailed`, no members or state, and exactly `The value could not be validated.`
Cause and Panic are inspectable local diagnostics, never client-safe text.
Cancellation/deadline errors propagate instead of becoming validation. Nil or
typed-nil validators return `ErrInvalidValidator`; invalid severities identify
`ErrInvalidSeverity`, and invocation failures identify `ErrValidatorFailed`.

## Apply command severity policy

`NewPolicy(SeverityOptions{Allowed: allowance, BlockOn: floor})` copies optional
severity pointers. Only values 0–3 are valid. Zero Policy retains only Error.
Explicit Allowed retains strictly greater severities; inclusive BlockOn supplies
the stricter threshold and always retains Unknown. Trusted Allowed=Error can allow
all supported severities only when no floor prevents it. Unsupported numeric
findings conservatively block. `Filter` returns copied blocking findings.

`AllowedFromHeader(values)` accepts exactly one trimmed signed decimal int32 in
0–3. The untrusted Error allowance is capped to Warning; duplicates, comma lists,
names, overflow and out-of-range values are ignored. An ignored header means
default policy, not permission to ignore errors. This deliberately closes the
native C# header parser's unsupported-enum bypass.

Filtering does not decide authorization and must not skip later permission checks.
For ownership and permission use [authorization policies](../authorization/index.md),
not a severity-filterable finding.

See [command results](../commands/results.md) and [query results](../queries/results.md)
for final envelope flags and status precedence. HTTP enforcement remains future work.
