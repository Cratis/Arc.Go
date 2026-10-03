# MongoDB snapshots and BSON codecs

This experimental module provides borrowed typed collection bindings, isolated
BSON mappings and bounded authorized snapshot rendering through Arc's query
pipeline. Live MongoDB/HTTP provider and real Chronicle sink compatibility are
not yet evidenced. Change streams are not implemented. No constructor connects
to MongoDB, starts workers, runs application codecs, or takes ownership of a client.

## API and ownership

- `NewCollection[T](client, CollectionOptions)` validates a named model struct and
  copies configuration. Supply explicit `Database`, `Name`, and `Ownership`.
  `SortFields` is an optional list of top-level JSON scalar field names; duplicate,
  unknown, array/nested and ambiguous PascalCase transport aliases are rejected.
  T must declare a nonnullable scalar BSON `_id`. There is no pluralization,
  exported driver handle, `Close`, or watch API.
- `ApplicationOwned` uses the binding's ordinary BSON codecs. `ChronicleOwned`
  requires an application `Release` callback on its renderer. It receives complete
  copied raw BSON, preserving ciphertext and lineage, before typed decoding or
  Arc interception. You own mapping and the actual Chronicle release call; this
  module does not manufacture a release from raw BSON or claim sink compatibility.
  Returned rows must retain count/order/identity; every failure suppresses both
  counts and data. `_id` must remain an ordinary declared scalar identity. Only
  approved cleartext sink fields may be filtered or declared sortable.
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
application. Use least-privilege read credentials and verified TLS. Drain all Arc
operations before your application disconnects the borrowed client.

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
`serialization.Optional` are rejected with `ErrUnsupportedModel`. BSON document
and value marshal/unmarshal hooks are rejected on every type and pointer method
set, including concepts and containers, without executing them; concepts retain
their required JSON/text conversion. Shared cached subgraphs cannot bypass the
maximum depth. Unknown stored fields are ignored; duplicate document keys are
rejected by ordinary materialization.

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

`ErrConfiguration`, `ErrUnsupportedModel`, `ErrValue`, `ErrLimit` and
`ErrReleaseRequired` are inspectable with `errors.Is`. Codec failures omit
application codec messages and document values. Provider operation errors use
safe text while retaining their causes for local inspection. Never expose the
unwrapped application/driver cause or panic diagnostics to clients.

## Authorized snapshots

Return `Find[T]{Filter: applicationFilter}` from a namespace query and register
`Find[T] -> []T` with `queries.RegisterRenderer`. Construct `NewRenderer` with
an explicit `RowFilter`. A nil callback fails construction; a nil returned filter
fails closed before database operations. An explicit `bson.D{}` intentionally
permits all rows. Filters are trusted application BSON using storage names, not
HTTP-supplied keys/operators. Do not mutate their graphs concurrently with
execution. Arc admission and tenant membership happen before renderer execution;
a tenant-selected database alone does not authorize its rows.

The renderer freezes filters once and combines them with `$and`. Count and find
use identical BSON, one resolved database/collection handle and simple collation,
with primary read preference and majority read concern. This is a configured read
profile, not topology validation: the driver can rewrite primary to
primaryPreferred for direct/Single topology. Use replica-set discovery when
primary selection matters. It counts before sorting
and windowing, maps declared JSON sort fields (and their PascalCase transport
aliases) to BSON overrides, and appends `_id:1` as a deterministic tie-breaker
unless `_id` is already sorted. Unknown active fields/directions fail with safe
Arc sorting validation. The server applies skip/limit; Arc does not page again.
Empty/out-of-range windows produce a nonnil empty slice with the authorized total.

Count and find are separate commands, **not one atomic snapshot**: concurrent
writes can change the selection between them. There are no hidden sessions,
transactions or provider retries; driver read retry settings remain yours.
Provision indexes for authorized counts and sorting; retention limits do not make
an unindexed count cheap.

| Option | Zero default and contract |
| --- | --- |
| `MaxItems` | 1000 retained rows; positive overrides must be below MaxInt32 |
| `MaxBSONBytes` | 16 MiB retained copied raw BSON |
| `Timeout` | 10 seconds for authorization/count/find/materialization; callbacks must honor context |
| Cursor cleanup | Separate detached five-second budget on every acquired cursor |

Negative options fail construction. Oversized page requests, unpaged selections,
retained byte/item bounds and overflowing Arc int32 page counts fail with
`ErrLimit`, never truncated success. Iteration copies raw bytes before advancing;
getMore/decode/release/cancellation/panic/cleanup failures return neither data nor
total. Cancellation is checked again after cleanup before success. Limits bound
retained documents/raw BSON, not arbitrary memory allocated by application
callbacks. Application callbacks must be concurrently safe and honor cancellation.

## Manual registration example

The compiled
[SnapshotExample source](https://github.com/Cratis/Arc.Go/blob/develop/integrations/mongodb/examples/snapshot/snapshot.go)
is complete at the **pipeline registration** scope: application-owned `Author`,
its `AllActive` namespace query, borrowed operation holder, explicit renderer,
verified-subject row filter and required tenant membership. It accepts your
configured driver client and membership authority; it does not create an HTTP
host, authenticate credentials, seed data or install indexes. Trusted ingress
must supply a verified principal and selected tenant before `queries.Perform`.
Its query identity is `Author.AllActive`.

From this module directory, execute the no-database registration example:

```bash
GOWORK=off go test -run ExampleSnapshotExample -v ./examples/snapshot
```

The example prints `true Author`. `TestSnapshotExampleExecutesExternalRegistrationAndDriverProfile`
also executes the registered namespace query through the real Arc pipeline and
renderer with the pinned driver's test-only wire responses; it checks exact
count/find predicates, coordinates, collation, majority reads, server windowing,
authorization denial and continued usability of the borrowed client. Those
responses are boundary evidence, not a MongoDB server or live query-semantics test.

## Evidence and next steps

`ExampleNewRegistry` compiles and executes the driver encoder workflow.
[Literal fixture provenance](https://github.com/Cratis/Arc.Go/blob/develop/integrations/mongodb/testdata/bson/README.md)
records independent BSON-spec/source-derived evidence, not codec-generated
capture. Unit and race tests require no database. Use the independent module
commands in the
[contribution guide](https://github.com/Cratis/Arc.Go/blob/develop/CONTRIBUTING.md).

[Arc.Go#22](https://github.com/Cratis/Arc.Go/issues/22) still requires task-owned
replica-set/HTTP provider evidence and real Chronicle sink mapping/release
compatibility, then separately specified watches. This checkpoint is not a
complete MongoDB integration or parity claim.
