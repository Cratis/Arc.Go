---
title: Query return types
description: Choose owning-model snapshot shapes, already-windowed pages or exact provider renderers.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

A query belongs to one named, nonpointer read-model struct `M`. Its client data
must be that model or a supported collection of it; unrelated models do not become
eligible because they happen to have the same fields.

## Snapshot matrix

| Performer output | Client data | Paging |
| --- | --- | --- |
| `M`, `*M` | Same declared shape | None |
| `[]M`, `[]*M`, arrays of either | Same collection shape | None by default |
| `Page[M]`, `Page[*M]` | `[]M`, `[]*M` | Already windowed; total supplied by performer |
| Provider output `Q` with exact `Renderer[Q,R]` | Supported owning-model `R` | Renderer-owned |
| Bare channel or iterator | Rejected | Adapt to an owned observable source |

A successful nil single model is ready-null and skips interception. `Page` is
unwrapped once and is never rendered or paged again. Plain slices stay unpaged
unless you explicitly register a renderer.

## Observable shapes

Use `RegisterObservable[M,A,O]` for `observable.Source[O]`; its emitted/rendered
model must satisfy the same owning-model rules. `State`, `Subject` and the owned
producer/iterator/channel-factory adapters implement this source contract.
`WithEnumerable[A]()` selects streaming-only behavior. Ordinary `Register`
never treats arbitrary channels as lifecycle-bearing queries. See
[observable queries](../observable-queries.md) and
[collection transfers](../change-stream.md).

## Render a provider query

Register a reusable renderer with `RegisterRenderer[Q,R]`, or select a per-query
override with `WithRenderer[A,Q,R]`. Registration keys are exact declared types,
not the first assignable runtime type. Build validates rendered ownership without
calling a factory. An unrelated raw output is rejected at Build unless an exact
renderer supplies an eligible result shape.

`Registration.ReturnType()` describes the raw performer output;
`DataType()` describes the rendered/unwrapped data. For `RegisterObservable`,
`ReturnType()` is the declared `observable.Source[O]`, `EmissionType()` is `O`,
and `DataType()` describes its rendered data. Value `ObservedCollection[T]`
emissions unwrap to `[]T`; pointer wrappers are rejected. The generic
`queries.Perform[R]` checks known compatibility before running application code.
Provider dependencies, counting and storage I/O belong inside rendering, not Build.

See [paging and sorting](paging.md) and the executed provider example in
`queries/example_test.go`.
