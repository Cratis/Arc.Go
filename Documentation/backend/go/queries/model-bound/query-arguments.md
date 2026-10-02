---
title: Query arguments
description: Bind flat typed arguments with explicit requiredness, defaults and raw presence.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Declare a named argument struct separately from your read model. Arc compiles its
exported fields when you register the query, so ordinary callers need no setters.
Use `NoArguments` for queries without caller input.

## Declare input rules

This declaration excerpt uses the `serialization` package for explicit presence:

```go
type SearchArguments struct {
    Category string                         `json:"category" query:"required"`
    Limit    int                            `json:"limit" query:"default=10"`
    Note     serialization.Optional[string] `json:"note" query:"preservePresence"`
}
```

Requiredness is explicit. Supplied zero and false are not missing. Required and
default cannot combine. Options use comma separation; backslash escapes comma,
equals and backslash in default text. Unknown, duplicated or malformed options fail.

| Input | Binding behavior |
| --- | --- |
| Missing | Typed default if supplied, otherwise zero/missing; required yields `rule` |
| GET empty | Omitted effectively unless `preservePresence` |
| QUERY null or empty string | Omitted effectively unless `preservePresence` |
| Supplied malformed scalar | `malformedRequest`; performer never runs |
| Direct `NewArguments` empty/null | Not transport-omitted; scalar conversion still applies |
| `RequestFor[A]` | Exact typed input; no conversion, omission or transport defaults |

`Request.Arguments()` and `QueryContext.Arguments()` retain raw missing/null/empty
presence separately from effective values. Binding constructs a fresh argument
model and publishes it only after every declared conversion succeeds.

## Supported conversions

- Primitive and named scalars, numeric enums, standard text codecs and valid concepts.
- Pointers and `serialization.Optional[T]` of supported inputs.
- Flat scalar collections and arrays; fixed arrays require the exact element count.
- Explicit `BindArgument` setters with custom `DecodeText`/`DecodeJSON`.

GET repeated collection values are split on commas and trimmed. QUERY arrays
preserve element boundaries; nested objects/arrays are invalid scalar elements.
JSON-node collections require explicit `json.RawMessage` bindings and custom JSON
conversion. No automatic nested DTO binding or enum-name inference is provided.

Wire names use `json` tags, otherwise Arc's acronym-friendly naming. Names match
case-insensitively. Duplicate names, reserved control names, unsupported field shapes
and invalid defaults fail registration. Unknown supplied arguments remain raw and
never invoke setters or choose dependencies. Explicit bindings replace matching
compiled fields; additional bindings retain their declared order.

For the envelope and control grammar, see [HTTP QUERY](../using-the-http-query-method.md).
For successfully bound model rules, see [query validation](../validation.md).
