# MongoDB bindings and BSON codecs

This experimental module lets you declare borrowed typed collection bindings and
validate BSON mappings independently of Arc's runtime. Query rendering, database
reads, release/publication and change streams are **not implemented** in this
checkpoint. No constructor connects to MongoDB, starts workers, runs application
codecs, or takes ownership of a client.

## API and ownership

- `NewCollection[T](client, CollectionOptions)` validates a named model struct and
  copies configuration. Supply explicit `Database`, `Name`, and `Ownership`.
  `SortFields` is an optional list of top-level JSON scalar field names; duplicate,
  unknown, array/nested and ambiguous PascalCase transport aliases are rejected.
  T must declare a nonnullable scalar BSON `_id`. There is no pluralization,
  exported driver handle, `Close`, renderer, or watch API.
- `ApplicationOwned` declares ordinary application documents. `ChronicleOwned`
  only reserves ownership metadata: it does **not** authorize publication. A
  future Chronicle renderer must preserve complete raw ciphertext and lineage,
  require Chronicle release before typed materialization/interception, and
  suppress counts and data on any release failure. No raw-document publication
  callback is provided or claimed here.
- `DatabaseName(base, tenant)` maps NotSet and exact named `Default` to the base;
  other tenants select `base+"+"+tenant`. Case and Unicode are preserved. Selecting
  a database is not membership authorization.
- `NewRegistry(types...)` validates nonrecursive type graphs (maximum depth 64,
  256 distinct types) and builds a new driver registry. Each binding has a private registry installed through
  driver v2's public `CollectionOptions.SetRegistry`, not client/global mutation.
  The registry returned by `NewRegistry` belongs to you; finish registration
  before use and never mutate it while it is in use. The profile covers only the
  registered graphs, not arbitrary unregistered driver types.

Client creation, TLS, credentials and eventual disconnect belong to your
application. Use least-privilege credentials and verified TLS. Arc must drain any
future operations before your application disconnects the borrowed client.

Database names are 1–63 UTF-8 bytes and reject MongoDB's cross-platform forbidden
characters (`/`, `\`, `.`, space, `"`, `$`, `*`, `<`, `>`, `:`, `|`, `?`, NUL).
Collections are nonempty UTF-8, reject `$`, NUL, a leading dot and the `system.`
prefix, and use
the unsharded 255-byte database-dot-collection namespace limit. Sharded
collections additionally require the server's 235-byte limit. Resolved tenant
coordinates are revalidated; nothing is sanitized, truncated, or lowercased.
Case-only tenant names cannot produce simultaneously usable isolated databases:
MongoDB rejects conflicting database casing. Prevent those collisions in your
application; preserving names is not permission to merge tenants silently.

## Storage profile

Shared persistence models require an explicit, nonempty `json` name on every
field. If `bson` is absent, the exact JSON name is the storage name. If present,
it is a deliberate storage override, for example `json:"id" bson:"_id"`.
Unlike C#'s default CLR property names, a JSON fallback here preserves lower-case
Go wire names. Use overrides to read an existing C# collection; do not rename
stored documents accidentally.

Only `omitempty` is accepted, never on `_id`. Anonymous/promoted/inline fields,
excluded or unexported fields, empty/duplicate/dotted/operator names, arbitrary
maps, interfaces/polymorphism, custom model codecs, decimal/opaque types and
`serialization.Optional` are rejected with `ErrUnsupportedModel`. Unknown stored
fields are ignored; duplicate document keys are rejected.

| Type | BSON write and materialization |
| --- | --- |
| String, bool | Native string/bool; invalid UTF-8 strings fail |
| int/int8/int16/int32 | Int32; out-of-Int32 writes fail rather than wrap |
| int64 | Int64 |
| uint8/uint16/uint32 | Int64 |
| uint/uint64 | Exact Decimal128 integer (not a general decimal API) |
| float32/float64 | Double; nonfinite/overflow values fail |
| Fundamentals UUID | Binary subtype 4, exactly 16 RFC/network-order bytes; dashed strings also read; subtype 3 fails |
| Fundamentals DateOnly | UTC noon DateTime; reads the UTC calendar date |
| Fundamentals TimeOnly | Epoch plus clock time, truncated to milliseconds; reads UTC time-of-day |
| time.Time | UTC Unix milliseconds; offset and sub-millisecond precision are lost; overflowing writes fail |
| Fundamentals TimeSpan | Invariant text; TimeSpan-backed concepts are a Go extension |
| Valid Fundamentals concept | Validated JSON underlying scalar; no reflective ConceptValue call or constructor |
| Pointer | Nil writes null; missing/null reads nil; nonnull creates a fresh destination |
| Slice | Nil writes an empty array; missing/null reads nonnil empty; pointer-to-slice remains nullable |

Numeric reads accept Int32, Int64, Double, Decimal128 and invariant numeric
strings with checked conversion. Fractions to integers, overflow and nonfinite
values fail instead of .NET Convert rounding/coercion. Legacy concept documents
must have exactly one `Value` or `value` field. Missing ordinary fields read as
zero; explicit null nonpointer scalars/concepts fail. Declared `_id` must be
present and nonnull, but zero identity is a value. A failed decode leaves the
entire destination, including prior pointer/slice contents, unchanged.

DateOnly's UTC noon is deliberate: C# at `7c1e780` uses unspecified-kind noon
through BsonUtils and can depend on the host timezone. These codecs do not claim
universal byte identity with that conversion. Domain codecs are application
code: their output is checked against Fundamentals' representation, but this
cannot prove that a codec represents the application's intended value.

`ErrConfiguration`, `ErrUnsupportedModel` and `ErrValue` are inspectable with
`errors.Is`. Codec failures omit application codec messages and document values.
Limits/release errors are not exported before those operations exist.

## Evidence and next steps

`ExampleNewRegistry` compiles and executes the driver encoder workflow.
[Literal fixture provenance](https://github.com/Cratis/Arc.Go/blob/develop/integrations/mongodb/testdata/bson/README.md)
records independent BSON-spec/source-derived evidence, not codec-generated
capture. Unit and race tests require no database. Use the independent module
commands in the
[contribution guide](https://github.com/Cratis/Arc.Go/blob/develop/CONTRIBUTING.md).

[Arc.Go#22](https://github.com/Cratis/Arc.Go/issues/22) still requires authorized
count/sort/paging rendering, bounded cursor cleanup, a proven Chronicle release
boundary, replica-set/HTTP provider evidence, then separately specified watches.
This checkpoint is not a complete MongoDB integration or parity claim.
