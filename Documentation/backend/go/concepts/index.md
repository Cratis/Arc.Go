---
title: Concepts and JSON values
description: Preserve UUIDs, calendar values, durations and missing input without losing wire meaning.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Use named Go values for domain meaning and the `concepts` package for scalar
contracts Go does not provide directly. Arc does not require a `Concept[T]` wrapper.

## Portable scalars

`UUID`, `DateOnly`, `TimeOnly` and `TimeSpan` are provided by
`github.com/cratis/fundamentals.go/concepts`, pinned at
`v0.0.0-20261002211957-66acfedfec9c`. Arc.Go's `concepts` package aliases these
shared types and forwards its existing constructors and parsers. You can pass
values between the two packages without conversion; existing Arc.Go imports
and wire formats are unchanged. `concepts.Concept[T]` also aliases the shared
concept declaration, so your models need only Arc.Go's `concepts` import.

| Type | Zero value | JSON output | Input |
| --- | --- | --- | --- |
| `concepts.UUID` | All-zero UUID | Lowercase dashed string | Dashed 8-4-4-4-12; either hex case |
| `concepts.DateOnly` | `0001-01-01` | `yyyy-MM-dd` | Invariant date, years 1–9999 |
| `concepts.TimeOnly` | Midnight | `HH:mm:ss.fffffff` | Seconds plus optional 1–7 fractional digits |
| `concepts.TimeSpan` | Zero duration | `[-][d.]HH:mm:ss[.fffffff]` | Invariant constant format |

Temporal precision is **100-nanosecond ticks**, not Go nanoseconds. TimeSpan keeps
the full signed int64 tick range; converting blindly to `time.Duration` can overflow.
DateOnly and TimeOnly have no time zone. Invalid parsing returns an error without
mutating an existing value. Null requires a pointer or `serialization.Optional`.

UUID bytes use RFC network order, not .NET `Guid.ToByteArray()` mixed-endian order.
`NewUUID` uses cryptographic version-4 generation. UUIDs are **not** Chronicle event
source identifiers: those may be arbitrary strings.

A new named type such as `type TaskID concepts.UUID` does not inherit codec
methods. Explicitly forward text/JSON methods, or compose the UUID in a domain
wrapper with its own codec. String/integer named types keep their scalar JSON kind.

## Declare a domain concept

Use `Concept[T]` when Arc should recognize a domain value as a particular scalar,
not merely a named Go primitive. This is an interface declaration, not a wrapper
or a replacement for your codecs. For example, this complete type declaration
requires imports of `encoding/json` and `github.com/cratis/arc.go/concepts`:

```go
type Title string

func (v Title) ConceptValue() string { return string(v) }
func (v Title) MarshalText() ([]byte, error) { return []byte(v), nil }
func (v Title) MarshalJSON() ([]byte, error) { return json.Marshal(string(v)) }
func (v *Title) UnmarshalText(data []byte) error {
    *v = Title(data)
    return nil
}
func (v *Title) UnmarshalJSON(data []byte) error {
    var value string
    if err := json.Unmarshal(data, &value); err != nil {
        return err
    }
    *v = Title(value)
    return nil
}

var _ concepts.Concept[string] = Title("")
```

The value declares its representation and implements both encoders; its pointer
implements both decoders. For a UUID-backed type, return `concepts.UUID` from
`ConceptValue` and forward all four codecs to that UUID. A marker without codecs
is invalid. `T` must be an exact supported primitive or shared scalar; concepts
cannot wrap another concept, and concept-bearing structs cannot embed fields.
Defined calendar types need forwarding codecs. A defined TimeSpan without a
marker is indistinguishable from an ordinary int64 and encodes as ticks.

Call `serialization.ValidateType(reflect.TypeFor[YourModel]())` during model
registration. It prepares cached field plans and checks pointers, collection
elements, map keys/values, and exported JSON fields without constructing values or
calling your methods. `Marshal` and `Unmarshal` also perform this check before
using a type, even for empty collections or omitted nil fields. Invalid declarations
preserve Fundamentals.Go's `concepts.ErrInvalidConcept` and `*concepts.TypeError`
for `errors.Is`/`errors.As`; import the Fundamentals package to inspect those errors.
Custom non-concept codecs are opaque, and interface values are checked at runtime.

Arc keeps using your codecs, never `ConceptValue`, to encode and bind values.
Pointer concepts retain Arc's null semantics; slices and string-key map values
retain their scalar encoding. Non-string map keys still need a custom codec.
HTTP GET/QUERY arguments use the query binding adapters; when authoring a concept,
make its text decoder accept the same canonical scalar as its JSON codec.
Arc's contract tests run Fundamentals.Go's `CheckJSON` on encoded concept fields;
responses do not pay that validation cost. Test your own codecs against `CheckJSON`
as well: static recognition cannot prove what a method will emit.

## Timestamps

`time.Time` keeps Go's standard JSON codec: RFC 3339 with an explicit offset,
UTC written as `Z`, and up to nine fractional digits with trailing zeros removed.
It does not adopt the 100-nanosecond precision of the calendar concepts above.
For example, `2026-10-02T03:04:05.123456789Z` retains all nine digits.

C# Arc uses System.Text.Json's default DateTime/DateTimeOffset codecs.
DateTimeOffset emits an offset (including `+00:00` for UTC) and at most seven
fractional digits. DateTime's suffix depends on its kind; unspecified DateTime and
other accepted C# input forms are not all supported by Go's RFC 3339 parser.
Timestamp parity is partial: normalize to a mutually supported offset and tick
precision before crossing languages, or supply a custom codec when an exact
DateTimeOffset wire spelling is required. There is no Arc.Go DateTimeOffset codec.

## Missing, null and zero

`serialization.Optional[T]` records three states:

- Its zero value is missing (`IsPresent() == false`).
- `serialization.Null[T]()` is present and explicitly null.
- `serialization.Some(value)` is present, including `0`, `false` and `""`.

Use `serialization.Unmarshal` on a fresh request value. Like C# Arc's body codec,
it binds the exact case-sensitive wire name. `{"Name":"Ada"}` does not bind to
`name`; case variants are unknown fields. Unknown fields are ignored, and exact
repeats of declared names are rejected instead of silently choosing the last one.
Failure leaves the target unchanged. Requiredness is application validation, not a
side effect of transport presence. This does not define future GET argument lookup.

Null for a plain `string` (including named string types) is rejected, unlike C#
string properties, which accept null under Arc's default serializer options.
Use `*string` to allow null, or `Optional[string]` to distinguish null from missing;
do not translate null to an empty string, which loses information. Other
non-nullable Go scalar targets also reject null.

Use `serialization.Marshal` to omit missing optional properties. Present-null
optional model properties are emitted as null to round-trip presence. This differs
from C# Arc's ordinary null-valued properties, omitted by `WhenWritingNull`;
clients must accept explicit null when consuming Optional-backed model properties.
Standalone `Optional.MarshalJSON` emits null for missing because a scalar encoder
cannot omit its enclosing property. Nil application payloads are omitted by result
envelopes; empty non-nil slices remain arrays. Initialize model slices explicitly
when clients require an empty array rather than an omitted nil property.

## Model codecs

Explicit `json` names win. Without a tag, Arc preserves leading acronyms (`ID`,
`URLValue`) and camelCases ordinary names (`FirstName` becomes `firstName`).
Tag an `ID` field with `json:"id"` to produce lowercase `id`.

Arc omits nil struct properties but retains scalar zeros. Explicit `omitempty` or
`omitzero` tags use Go's `encoding/json` omission rules: `omitempty` keeps zero
structs, while `omitzero` honors a type's own `IsZero()` method, including pointer
receivers. Map null values and collection null elements stay
null. Numeric enum-like Go types remain numbers. Named floating-point literals use
`"NaN"`, `"Infinity"` and `"-Infinity"`. Untyped numbers bind as `json.Number`,
not float64, so large integer values retain their precision.

The foundation codec supports exported fields, pointers, collections and string-key
maps. Embedded structs, including unexported ones, promote their exported fields
with `encoding/json` precedence: shallower fields win, tags break same-depth ties,
and equally dominant promoted names are ignored. Decoding cannot allocate a nil
unexported embedded pointer; use value embedding or an exported embedded type.
Custom `json.Marshaler`/`json.Unmarshaler` implementations take precedence over
`encoding.TextMarshaler`/`encoding.TextUnmarshaler`; text codecs use JSON strings.
Pointer-receiver marshalers are honored for addressable values, such as fields
reached through a pointer and slice elements. Like `encoding/json`, an
unaddressable value does not acquire pointer-receiver marshal methods: pass a
pointer when needed. Custom codecs own their wire format and recursion safety.
Non-string dictionary keys and unsupported tag options return errors; do not infer
polymorphism or schema generation from reflection. Framework encoding/binding is
limited to 64 levels, including nested Optional, validation, result and change-set
values. HTTP hosts must additionally bound request bytes before binding; this
package does not host HTTP.

See the [parity map](../../../parity.md) for input restrictions and remaining gaps,
and the [executable foundation example](../../../../example_test.go) for scalar use.
