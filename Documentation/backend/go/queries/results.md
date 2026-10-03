---
title: Query results
description: Represent ready, pending, paged and delta query outcomes without losing null semantics.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

A query result separates **no value yet** from a value that happens to be null.
Use `queries.Success(correlationID, data)` for an emitted value and
`queries.NotReady[T](correlationID)` for an observable that has not emitted.
Neither starts a query or subscription.

## Readiness and status

`queries.NewResult(details, data)` accepts explicit `queries.Details` and
`serialization.Optional[T]`. Set `Authorized` and `Ready` intentionally: their
zero values are false. `Data()` reports local presence independently of readiness.
A successful nil value is ready, but `data` is omitted from JSON.

`IsSuccess` requires readiness, authorization, no validation findings and no
exception messages. Standard status selection is **200**, **403**, **400**, **202**
(not ready), then **500**, in that order. Like C#'s helper, a contradictory not-ready
exception result still selects 202; normal producers should not construct that state.

HTTP hosting separately selects **401** for credential rejection and **400** for
QUERY reader failures, even when they carry exceptions. First-result waits and
**408** remain unsupported; snapshot reads never pretend to wait. `StatusCode()` is not a universal HTTP
error mapper.

## Paging and change sets

`PagingInfo` always appears as `{ page, size, totalItems, totalPages }`.
Its zero value is not paged. `Page` is zero-based. `TotalPages()` uses the C#
double-precision ceiling; zero size gives zero total pages. Page/size are int32,
total items is int64. Page-count overflow returns MinInt32 deterministically;
this is not a claim about a particular C# runtime's unchecked overflow behavior.
This is response metadata only: no request reader, sorting, validation or automatic
slicing runs here.

`Details.ChangeSet` optionally supplies `queries.ChangeSet` with `Added`,
`Replaced` and `Removed` item arrays. Empty arrays always serialize as `[]`.
Removed entries are full items, not just IDs. This DTO does not compute differences,
maintain a baseline or implement SSE/WebSocket framing.

## Serialization and ownership

Query envelopes always include correlation, flags, required validation/exception
arrays, stack string and paging. Unlike commands, they never include
`authorizationFailureReason`. Null `data` and `changeSet` are omitted; scalar zeros
and empty non-nil collections are preserved.

Constructors and `Details()` copy framework-owned arrays and change-set slices.
Application data, change-set items and validation state are borrowed; do not mutate
them during concurrent use. Results are output-only values, not remote JSON decoders.

See [command results](../commands/results.md), [concept codecs](../concepts/index.md)
and [the parity map](../../../parity.md) for the exact implemented boundary.
