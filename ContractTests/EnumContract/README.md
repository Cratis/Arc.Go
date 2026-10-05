# Int32 enum contract fixture

This fixture compares an opt-in Go JSON parser with **Arc 22.48.2 / Fundamentals
7.19.6**, then sends the same numeric results through the real **TypeScript
Fundamentals 7.22.0** serializer. It is a bounded checkpoint for
[Arc.Go #32](https://github.com/Cratis/Arc.Go/issues/32), not complete enum,
generator, OpenAPI or Chronicle parity.

## Contract and limits

`serialization.NewInt32Enum` copies original parse names for a named `int32`.
Call `ParseJSON` from that type's `UnmarshalJSON` method; ordinary JSON encoding
already writes its integer value. `serialization/enum_test.go` contains a compiled
example. Construction rejects empty, non-ASCII and case-ambiguous declarations.
These are explicit Go admission restrictions, not restrictions on CLR enum names.

The pinned converter has **different input and output domains**:

- Numeric JSON accepts declared values only, including aliases and negative values.
- Case-insensitive original names and comma-separated names are accepted. Names
  combine by bitwise OR even on an enum without `[Flags]`.
- Decimal strings accept *any* Int32, including unknown values and flag bits.
- Writes accept *any* Int32. An unknown value can therefore be written as a number
  that the same converter rejects on reading. This asymmetry is intentional parity.
- Scalar null fails. Nullable wrappers own null; Arc Go still omits nil object
  properties and preserves null collection elements only for nullable elements.
- Parsing does not use TypeScript export renames. `Read` is accepted; the frontend
  export `reader` is not an additional input name.

The newer Fundamentals flags fix is **not** part of this pinned profile. Ordinary
Go named integers retain their existing open underlying domain; merely declaring
constants never installs this parser. Wider backing types are negative/control
observations, not admitted Go codecs. Query-string binding is not covered.

The client fixture uses `@field(Number)` and real hydration, not an enum parser
simulation. Unknown numbers remain numbers. Number metadata does not validate
incoming names: a string passes through as a string. Explicit null hydrates but
pinned client reserialization throws; this fixture does not hide that limitation.
Backend integer bounds, JavaScript safe-number bounds and signed 32-bit bitwise
bounds are distinct. No wide-integer frontend compatibility is claimed.

## Sources and evidence

- Actual packaged `ConfigureArcDefaults`:
  [Arc source 7c1e780](https://github.com/Cratis/Arc/blob/7c1e78075b737df64f69fddfaae83374f75e3612/Source/DotNET/Arc.Core/JsonSerializerOptionsConfiguration.cs).
- Actual packaged converter:
  [Fundamentals source 14037b1](https://github.com/Cratis/Fundamentals/blob/14037b1ff8346b7944ad48065b8f66feca80dab5/Source/DotNET/Fundamentals/Json/EnumConverter.cs).
- `reference/packages.lock.json` fixes exact package contents. `reference.py`
  checks restored package hashes, SDK **10.0.401**, runtime **10.0.12**, both
  assembly source revisions, the complete input inventory and independent
  reserialization outcomes. It refuses other installed SDK versions rather than
  downloading or rolling forward.
- `profile.json` is actual C# output: 75 reads and 11 independent writes. Go
  compares 65 Int32 reads and 8 writes; wider controls remain outside admission.
  Source/corpus/lock hashes detect stale captures. Integers are retained as exact
  decimal strings; failure categories contain no exception messages.
- `normalized.json` states directional domain and member facts. It is a **fixture
  contract, not current generator output or a published OpenAPI document**.
- `frontend/model.ts` is a hand-authored fixture of the supported proxy shape,
  **not generated output**. Strict compilation and actual hydration use the
  existing `ProxyComparison/package-lock.json`; no new frontend dependency pin.

[Chronicle.Go's reviewed enum evidence at 747c1eb](https://github.com/Cratis/Chronicle.Go/tree/747c1eb3a015a583b4fd2ffa51b6c6ab3a185087/serialization/testdata/enum)
uses the same Fundamentals package but a different owning serializer/schema
profile. It informed this boundary; it is not copied, rerun, or treated as proof
of Arc or kernel behavior. No Chronicle dependency is added here.

## Reproduce

Run separate bounded build/test phases from the repository root. Use installed
SDK 10.0.401 and runtime 10.0.12. The initial restore may use the installed NuGet
cache as `--source`; subsequent restores must be locked.

```sh
dotnet restore ContractTests/EnumContract/reference/Reference.csproj --locked-mode \
  --artifacts-path .ai-work/enum-reference
dotnet build ContractTests/EnumContract/reference/Reference.csproj -c Release \
  --no-restore --artifacts-path .ai-work/enum-reference
python3 -B ContractTests/EnumContract/reference.py --artifacts .ai-work/enum-reference
GOWORK=off GOTOOLCHAIN=local go test -count=1 -timeout=90s ./serialization ./ContractTests/EnumContract
node ContractTests/EnumContract/frontend/compile.mjs
node --test --test-timeout=30000 ContractTests/EnumContract/frontend/enum.test.mjs
```

`--capture` deliberately replaces `profile.json` after successful reference
execution; review all resulting changes. Normal mode compares without writing.
The frontend commands require the existing locked ProxyComparison installation.
`ARC_ENUM_NODE_MODULES` may point to another **read-only** installation of those
same pinned packages; these scripts neither install nor modify dependencies.

## Deferred generator integration

The current generator intentionally still refuses these `UnmarshalJSON` methods
as opaque custom codecs. No generator admission is added by this checkpoint.

A subsequent owned generator change must preserve original parse names alongside
Go declaration names and renamed TypeScript exports, identify this exact opt-in
parser profile without admitting arbitrary codecs, and normalize input/output
separately. Keep ordinary enum domains open; do not turn output schemas into a
list of declared constants. Preserve exact alias values and ordering. Apply
safe-number and flag-bitwise checks only to frontend admission. Replace this
hand-authored client fixture with production-generated consumer evidence before
claiming generated enum support. Shared graph, emitter and parity edits are left
to the integration owner.
