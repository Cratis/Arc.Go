# QUERY request schema and reader boundaries

The request schema describes the built-in reader used by the bounded,
argument-free, nonpageable snapshot projection for
[Arc.Go#24](https://github.com/Cratis/Arc.Go/issues/24). It is not an application
argument binder or proof of general HTTP acceptance.

## Sorting activation

The tools module pins Arc.Go
`v0.0.0-20261003142617-78ebbf8fdff8`. Its `queries/request_query.go` reads the
sorting object and decodes `field` first. Only `field != ""` causes it to decode
`direction` and call `ParseSortDirection` in `queries/sorting.go`.

Consequently, absent, empty and null fields leave sorting inactive. Direction
values such as `42`, `[]`, `{}`, `true`, null and unrecognized strings are ignored
in those cases. A nonstring, nonnull field remains malformed regardless of the
direction. A nonempty field, including whitespace, requires a string or null
direction. Missing/null direction defaults to ascending; asc, ascending, desc
and descending are accepted case-insensitively. Other strings produce a semantic
sorting error rather than a JSON type error, so the request schema still admits
those strings.

The emitted conditional uses the same case-alias patterns for field activation
and direction constraints as for ordinary known members. The real reader's
`strings.ToLower` behavior, including U+0130, is exercised by the paired tests;
these are Go-reader witnesses, not newly captured C# behavior.

`openapi_query_boundary_test.go` runs the raw bodies through the pinned reader,
asserting malformed versus semantic errors and the resulting sorting defaults.
The explicitly selected `openapi_query_boundary_consumer_test.go` runs the same
bodies against the actual emitted request schema at
`/paths/~1api~1plain/x-cratis-query/operation/requestBody/content/application~1json/schema`.
The existing Stage 1 check compiles that pointer in the whole authoritative
OpenAPI document without reconstructing a standalone schema, loading external
resources or reserializing a kin structural view.

## Raw JSON boundaries

JSON Schema validates JSON values, not the original token stream. Both `page`
and `pageSize` require int32 decoding in the pinned reader. The paired witnesses
show that `1` is accepted, while `1.0`, `1e0` and `1E+0` are malformed for that
reader despite all four being exactly the mathematical integer one and valid
against the emitted integer schema. Integer bounds and fractional-value controls
remain unchanged. A schema pass does not establish lexical reader acceptance.

Likewise, duplicate known names (including case aliases) and trailing JSON are
raw-parser failures. The separate reader witnesses retain these failures even
when sorting is inactive. They are not claimed as schema assertions, and the
schema tests do not decode duplicates into a map and present that as proof of
duplicate handling.

This correction does not admit unsupported codecs, framework-generated routes,
streaming shapes or new renderer capabilities. Whole-document refusal remains
required; no existing numeric fixtures or C# capture/provenance files change.
