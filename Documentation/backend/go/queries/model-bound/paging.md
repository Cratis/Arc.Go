---
title: Paging and sorting
description: Count authorized rows before stable ordering and paging without changing ordinary-list behavior.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

A paging request does not automatically page every list. Choose a provider renderer
when you want the source to count, order and window, or return `Page[T]` when your
query already did that work.

## Request controls

| Control | GET | QUERY |
| --- | --- | --- |
| Paging activation | Any parsed int32 `pageSize`, including zero/negative | Positive integer int32 `pageSize` only |
| Page | Invalid/missing defaults to zero | Integer token required when present |
| Sorting | Nonempty `sortby` and `sortDirection` together | Nonempty `sorting.field`; omitted/null direction is ascending |
| Direction grammar | `asc`, `ascending`, `desc`, `descending`, case-insensitive | Same; explicit empty fails |

The pipeline validates active page ≥ 0 and size > 0, using C# concept messages
and `page`/`size` members. This intentionally enforces rules that the older C#
request path did not always execute. `Paging.Skip()` uses int64 multiplication
and clamps to `[0, MaxInt32]`.

## Render at the source

A `Renderer[Q,R]` receives `QueryContext.Parameters()`. Apply row authorization,
count the authorized selection, establish an order, then window. Return
`RendererResult[R]{Data: data, TotalItems: total}`. Filtering after the window
or merely masking rows can leak totals and produce incorrect pages.

`Page[T]` supplies already-windowed items and the full authorized total. The
pipeline never applies a second window. Unpaged results preserve zero `PagingInfo`.
For active rendered/page output, metadata includes page, size, total items and
computed total pages.

## Opt into in-memory rendering

`NewSliceRenderer[T]` accepts `SortColumn[T]` entries with exact wire-field names
and comparison callbacks. It copies slice membership, counts before paging,
stable-sorts and windows. It does not deep-clone items, or turn an expensive
full-data fetch into provider-side paging.

Only registered columns can sort. An unknown active field yields safe validation,
not reflected property access. Wire names stay unchanged; the renderer also accepts
the corresponding PascalCase transport spelling. Comparators must be concurrent-safe
and must not mutate models. Plain slices without a renderer are neither counted
nor sliced, even when controls request paging.

The `ExampleWithParameters_page` example in `queries/example_test.go` executes an
already-windowed page. Continue with [interception](../read-model-interception.md)
to transform output after rendering.
