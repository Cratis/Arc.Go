---
title: Validation findings
description: Encode client-visible severities, member paths and open rejection reasons.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Use `validation.Result` for feedback callers can act on, not exception messages.
The foundation supplies the result contract; validators and severity filtering
arrive with the command/query pipelines.

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
A future pipeline may filter findings before constructing its final result;
serialization never silently removes warnings or information.

Messages and state are not automatically redacted. Do not put credentials, parser
internals or other sensitive details in these fields. `Clone()` copies members and
reason-detail storage, but continues to borrow application `State`.

See [command results](../commands/results.md) and [query results](../queries/results.md)
for how findings affect the envelope and standard status precedence.
