---
title: Concepts and JSON values
description: Preserve UUIDs, calendar values, durations and missing input without losing wire meaning.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Use named Go values for domain meaning and the `concepts` package for scalar
contracts Go does not provide directly. Arc does not require a `Concept[T]` wrapper.

## Portable scalars

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

## Missing, null and zero

`serialization.Optional[T]` records three states:

- Its zero value is missing (`IsPresent() == false`).
- `serialization.Null[T]()` is present and explicitly null.
- `serialization.Some(value)` is present, including `0`, `false` and `""`.

Use `serialization.Unmarshal` on a fresh request value. It binds case-insensitive
wire names, ignores unknown fields, and rejects duplicate declared names instead
of silently choosing the last one. Failure leaves the target unchanged. Requiredness
is application validation, not a side effect of transport presence.

Use `serialization.Marshal` to omit missing optional properties. Standalone
`Optional.MarshalJSON` emits null for missing because a scalar encoder cannot omit
its enclosing property. Nil application payloads are omitted by result envelopes;
empty non-nil slices remain arrays.

## Model codecs

Explicit `json` names win. Without a tag, Arc preserves leading acronyms (`ID`,
`URLValue`) and camelCases ordinary names (`FirstName` becomes `firstName`).
Tag an `ID` field with `json:"id"` to produce lowercase `id`.

Arc omits nil struct properties but retains scalar zeros. Explicit `omitempty` or
`omitzero` tags opt into omission. Map null values and collection null elements stay
null. Numeric enum-like Go types remain numbers. Named floating-point literals use
`"NaN"`, `"Infinity"` and `"-Infinity"`. Untyped numbers bind as `json.Number`,
not float64, so large integer values retain their precision.

The foundation codec supports ordinary exported structs, pointers, collections and
string-key maps. Custom `json.Marshaler`/`json.Unmarshaler` implementations own their
format. Embedded fields, non-string dictionary keys and unsupported tag options
return errors; do not infer polymorphism or schema generation from reflection.
Recursive encoding/binding is limited to 64 levels. HTTP hosts must additionally
bound request bytes before binding; this package does not host HTTP.

See the [parity map](../../../parity.md) for input restrictions and remaining gaps,
and the [executable foundation example](../../../../example_test.go) for scalar use.
