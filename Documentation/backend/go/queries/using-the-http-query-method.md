---
title: GET and HTTP QUERY readers
description: Normalize GET controls and QUERY envelopes while preserving safe parse errors and no-store metadata.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Use `ReadGET(url.Values)` or `ReadQUERY(body)` to normalize transport data before
pipeline admission. These are request readers, not HTTP handlers. Hosting must
limit body size and apply status/cache metadata; these functions do not expose routes.

## QUERY envelope

```json
{
  "arguments": { "category": "books", "ids": [1, 2] },
  "paging": { "page": 0, "pageSize": 10 },
  "sorting": { "field": "name", "direction": "ascending" }
}
```

The body must contain exactly one object. Null/array roots, trailing JSON values,
case-insensitive duplicate declared envelope/control members and duplicate argument
names fail. Unknown envelope members are permissive. Argument raw JSON is retained;
conversion waits until the typed registration is known.

Paging values must be integer tokens within int32 range; decimal/exponent tokens,
strings and null integer members fail parsing. Positive size enables paging.
Omitted/null sort direction defaults ascending; explicit empty is invalid when
sorting is active. Arrays preserve scalar element boundaries. Nested scalar elements
fail binding; JSON-node collections require explicit custom bindings.

## GET controls

Reserved names are case-insensitive: `page`, `pageSize`, `sortby`, `sortDirection`,
`waitForFirstResult`, and `waitForFirstResultTimeout`. Repeated or case-folded duplicate
controls fail; wait controls are excluded from arguments but no waiting is implemented.
Repeated caller collections collapse through comma splitting and trimming.

Parsed int32 size enables paging even for zero/negative size; active pipeline
validation then rejects invalid page/size. Invalid/missing page defaults zero.
Sorting requires both nonempty field and direction. Only asc/ascending/desc/descending
are accepted, case-insensitively.

## Reader metadata and errors

`QueryStringRequestReader` implements method GET with empty cache-control metadata.
`BodyRequestReader` implements QUERY with `ResponseCacheControl() == "no-store"`.
Hosting must send that header on **success and reader failure**, and separately map
malformed QUERY syntax to 400. Do not rely solely on result status precedence.

`ReadError.Malformed` distinguishes syntax/envelope errors from semantic sorting
findings. The original parser cause remains inspectable. `ArgumentError` carries
safe `rule` or `malformedRequest` findings; `SortingError` carries a safe field finding.
Production exception text must never expose parser input or private diagnostics.

See [argument binding](model-bound/query-arguments.md) for effective presence rules
and [paging](model-bound/paging.md) for provider behavior.
