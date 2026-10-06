---
title: Int32 enums on the wire
description: Accept the names, numbers and comma-separated flags that C# Arc clients send for an int32 enum, while still writing numbers.
---

A Go named integer writes and reads plain numbers. C# Arc's JSON configuration is
more permissive on input: a client may send an enum as its number, its name in any
letter case, several names separated by commas, or a decimal string. If your Go
backend must accept the same input, mark a named `int32` type with
`//arc:enum parse=int32`. Arc-gen generates its parser declaration and
`UnmarshalJSON` method over `serialization.NewInt32Enum`.

Status: **Partial**. The parser matches Arc 22.48.2 with Fundamentals 7.19.6
(`EnumConverter.cs`). Generation currently requires a package containing commands
or read models. Enum-only packages and imported generated enum admission are not
implemented yet.

## Opt a type in

This excerpt follows the compiled generator consumer in
`tools/internal/artifacts/testdata/enum`. Put it in a package containing your Arc
commands or read models:

```go
//arc:enum parse=int32 members=Read:Reader
type State int32

const (
    Zero  State = 0
    Read  State = 1
    Write State = 4
    Alias State = 4
)
```

Run arc-gen in that module to generate `zz_arc_generated.go`. Do not write your
own `UnmarshalJSON` on an opted-in type: generation diagnoses it before publishing
anything. Remove the method and regenerate to resolve the conflict. Other custom
codec methods cannot be combined with this profile either.

Reading `{"state":"Read, Write"}` and writing it back produces `{"state":5}`. Run the
generated consumer with:

```bash
cd tools
go test -run '^TestEnumGeneratedConsumerAndStableTypeScript$' ./internal/artifacts
```

There is no global registration and no marshal hook: normal encoding already writes
the number. Declaring constants alone never installs the parser.

`NewInt32Enum` copies the map. Names must be nonempty ASCII identifiers that are
unique ignoring case; several names may share a value, as `Write` and `Alias` do
here. Name Go constants with the **original C# member names**. The `members`
option renames only TypeScript exports: `Read:Reader` still parses `"Read"`, not
`"Reader"`. TypeScript output does not change when you enable `parse=int32`. The
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
real TypeScript Fundamentals 7.22.0 serializer. The tools generated-consumer test
also runs the State read corpus through generated `UnmarshalJSON` and ordinary
Arc binding, verifies failure preserves the receiver, and checks byte-stable
TypeScript output. See its
[fixture README](https://github.com/Cratis/Arc.Go/blob/develop/ContractTests/EnumContract/README.md)
for the exact profile and the [concepts page](index.md) for other wire values.
