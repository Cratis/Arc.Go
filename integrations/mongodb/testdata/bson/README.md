# Independent BSON fixtures

`fixtures.json` contains literal, source-derived BSON documents with one `v`
field (or a missing field). They are not output captured from this codec or
MongoDB/C# execution. Tests decode the literals and compare writes to them.

Sources:

- [BSON specification](https://bsonspec.org/spec.html): document length includes
  the four-byte length and final zero; numbers and lengths are little-endian;
  UUID uses binary type `05`, length 16, subtype `04`.
- [Arc ConceptSerializer at 7c1e780](https://github.com/Cratis/Arc/blob/7c1e78075b737df64f69fddfaae83374f75e3612/Source/DotNET/MongoDB/ConceptSerializer.cs):
  primitive concept writes and legacy documents with `Value` or `value`.
- [Arc DateOnlySerializer at 7c1e780](https://github.com/Cratis/Arc/blob/7c1e78075b737df64f69fddfaae83374f75e3612/Source/DotNET/MongoDB/DateOnlySerializer.cs):
  noon DateTime storage. These fixtures deliberately use **UTC noon**, not the
  C# unspecified-kind/host-timezone conversion.
- [Arc TimeOnlySerializer at 7c1e780](https://github.com/Cratis/Arc/blob/7c1e78075b737df64f69fddfaae83374f75e3612/Source/DotNET/MongoDB/TimeOnlySerializer.cs):
  Unix epoch plus clock time, millisecond precision.

The asymmetric UUID payload is exactly
`00 11 22 33 44 55 66 77 88 99 aa bb cc dd ee ff`, not .NET mixed-endian bytes.
The leap-day UTC-noon timestamp is 1,709,208,000,000 milliseconds since epoch
(`00 56 bc f4 8d 01 00 00`). The clock timestamp is 45,296,789 milliseconds
(`95 2c b3 02 00 00 00 00`). The pre-epoch timestamp is signed -1.
These timestamp payloads were calculated independently with Python's standard
`datetime` and `struct.pack("<q", value)`, not a BSON library.

Missing/null slices normalize to an empty slice under this Go storage profile;
missing/null pointers remain nil. Zero scalar values and empty strings remain
values. These are materialization fixtures, not live-provider or Chronicle
ciphertext-release evidence.
