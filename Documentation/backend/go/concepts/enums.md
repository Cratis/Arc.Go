---
title: Int32 enums on the wire
description: Accept the names, numbers and comma-separated flags that C# Arc clients send for an int32 enum, while still writing numbers.
---

A Go named integer writes and reads plain numbers. C# Arc's JSON configuration is
more permissive on input: a client may send an enum as its number, its name in any
letter case, several names separated by commas, or a decimal string. If your Go
backend must accept the same input, opt a named `int32` type in to
`serialization.Int32Enum`.

Status: **Partial**. The parser matches Arc 22.48.2 with Fundamentals 7.19.6
(`EnumConverter.cs`). The generator does not yet produce it; you write the
`UnmarshalJSON` method yourself, and arc-gen still refuses such a type as an opaque
custom codec.

## Opt a type in

This code is from `ExampleInt32Enum` in `serialization/enum_test.go`:

```go
type state int32

var stateCodec, stateCodecError = serialization.NewInt32Enum(map[string]state{"None": 0, "Read": 1, "Write": 4, "Alias": 4})

func (s *state) UnmarshalJSON(data []byte) error {
    if stateCodecError != nil {
        return stateCodecError
    }
    value, err := stateCodec.ParseJSON(data)
    if err != nil {
        return err
    }
    *s = value
    return nil
}
```

Reading `{"state":"Read, Write"}` and writing it back produces `{"state":5}`. Run the
example with:

```bash
go test -run ExampleInt32Enum ./serialization
```

There is no global registration and no marshal hook: normal encoding already writes
the number. Declaring constants alone never installs the parser.

`NewInt32Enum` copies the map. Names must be nonempty ASCII identifiers that are
unique ignoring case; several names may share a value, as `Write` and `Alias` do
here. Use the **original C# member names**, not renamed TypeScript exports. The
parser is immutable and safe for concurrent use. A nil or zero parser rejects all
input.

## What the parser accepts

| JSON input | Result |
| --- | --- |
| `1` | Accepted only if a declared value is 1 |
| `9` (undeclared) | Rejected |
| `"read"`, `"READ"` | Case-insensitive original name |
| `"Read, Write"` | Bitwise OR of the names, even without a C# `[Flags]` attribute |
| `"9"`, `"-3"` | Any int32 decimal string, including undeclared values |
| `null` | Rejected; use a pointer or `serialization.Optional` for nullable values |
| `"Unknown"`, `""`, a fraction or out-of-range number | Rejected |

On every failure `ParseJSON` returns zero and `serialization.ErrInvalidEnumValue`.
The error never includes the supplied payload, and a failed comma-separated list never
returns a partial combination.

## Input and output domains differ

Writing accepts any `int32` value. An undeclared value is therefore written as a
number that the same parser rejects on input. This asymmetry matches the pinned C#
converter and is deliberate. Validate the value before you send it if a client
must be able to post it back.

Later Fundamentals versions changed numeric flags admission; this profile does not
follow them. Ordinary Go integer types keep their open domain, other backing widths
are unsupported, and query-string binding is not covered.

## Evidence

`serialization/enum_test.go` covers declaration validation, copying, zero and nil
parsers, failure atomicity, null presence, collections and concurrent parsing.
`ContractTests/EnumContract` compares 65 int32 reads and 8 writes with actual C#
output captured from the pinned packages, and sends the numeric results through the
real TypeScript Fundamentals 7.22.0 serializer. See its
[fixture README](https://github.com/Cratis/Arc.Go/blob/develop/ContractTests/EnumContract/README.md)
for the exact profile and the [concepts page](index.md) for other wire values.
