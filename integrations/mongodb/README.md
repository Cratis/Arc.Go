# MongoDB snapshots, observations and BSON codecs

This experimental module provides borrowed typed collection bindings, isolated
BSON mappings and bounded authorized snapshot rendering through Arc's query
pipeline. MongoDB 8.0.15 single-member replica-set/HTTP contracts have task-owned
provider evidence. Real Chronicle sink compatibility remains unproved. Database
invalidation sources have deterministic unit coverage only; live change-stream
behavior remains unverified. No constructor connects
to MongoDB, starts workers, runs application codecs, or takes ownership of a client.

## API and ownership

- `NewCollection[T](client, CollectionOptions)` validates a named model struct and
  copies configuration. Supply explicit `Database`, `Name`, and `Ownership`.
  `SortFields` is an optional list of top-level JSON scalar field names; duplicate,
  unknown, array/nested and ambiguous PascalCase transport aliases are rejected.
  T must declare a nonnullable scalar BSON `_id`. There is no pluralization,
  exported driver handle or collection `Close`; `Observe` uses a separate watcher.
- `ApplicationOwned` uses the binding's ordinary BSON codecs. `ChronicleOwned`
  requires an application `Release` callback on its renderer. It receives complete
  copied raw BSON, preserving ciphertext and lineage, before typed decoding or
  Arc interception. You own mapping and the actual Chronicle release call; this
  module does not manufacture a release from raw BSON or claim sink compatibility.
  Returned rows must retain count/order/identity; duplicate decoded identities
  fail closed when distinct BSON IDs coerce to the same Go identity. Every failure
  suppresses both counts and data. `_id` must remain an ordinary declared scalar identity. Only
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

## Database invalidation sources

`NewWatcher(applicationLifetime, borrowedClient, WatcherOptions{})` constructs a
lazy owner. `Observe(watcher, collection, Find[T]{Filter: applicationFilter})`
freezes the trusted selection with the binding's registry; it performs no reads
and retains no performer scope. Neither construction starts a reader. Never use
a request context as the shared watcher's lifetime, and never bind `Find` from
HTTP-authored BSON. Its JSON methods use a validated base64 BSON-byte envelope,
not a lossy interface-valued JSON predicate. Decode is failure-atomic.

Manually register `queries.RegisterObservable[M,A,mongodb.Find[M]]`, paired with
`queries.WithRenderer[A,mongodb.Find[M],[]M]` for the **same collection binding**.
The complete compiled
[ObservationExample source](https://github.com/Cratis/Arc.Go/blob/develop/integrations/mongodb/examples/observation/observation.go)
shows application lifetime, membership, a verified-subject row filter and fresh
callback-scoped resource access on each emission. It supplies no HTTP host,
authentication, database data or indexes. Its no-database registration example is:

```bash
GOWORK=off GOTOOLCHAIN=local go test -run ExampleObservationExample -v ./examples/observation
```

It prints `true []observation.Author`; this proves registration, not a live watch.
Opaque provider emissions remain manually registered, not generated proxies.

Each `Source.Open` reserves an independently owned subscriber. The shared owner
establishes a database `$changeStream` with ordinary public `Aggregate` and
`mongo.Cursor`, **not** `Database.Watch` with its hidden automatic resume. The
initial immutable `Find` instruction becomes available only after cursor
establishment. Registration, initial queueing and namespace fanout share one
ordering lock. Inserts, updates, replacements and deletes invalidate the whole
selection regardless of post-image membership. Arc rechecks authorization,
executes the existing renderer/count/find/release, then interception and emission
guards. Queued work is not cleared when rendering finishes. Count/find remain
separate, non-atomic commands, and duplicate equivalent snapshots are allowed.

This is **Source only**, not `CurrentSource`. Plain non-wait observable snapshots
remain pending; waiting and streaming execute the initial renderer baseline. No
synthetic model or empty-ready value is emitted. Ready empty results require a
successful renderer, and out-of-range pages retain its authorized total. There
is no authorized-row cache, incremental membership/page tracker or idle-policy
revocation polling.

Watch identity is the borrowed client pointer plus resolved database name.
Different models/collections on one database share a reader; different clients
or resolved tenant databases do not. There is no global registry or client cache.
Without Arc query metadata, direct Open uses NotSet and an unnamed query key.
Database selection is not tenant membership authorization.

Zero options select: 32 databases, 1024 total subscribers, 64 subscribers per
resolved database/collection/logical-query name, 16 queued markers, 64 KiB frozen
filter, ten-second opening and five-second cursor-cleanup budgets. Negative
options fail. Opening and retired-but-unclosed streams count against admission;
new source instances cannot bypass a query limit. Idle live readers count against
the database limit until owner shutdown. Driver batches are independently bounded
to 64. Queues retain only markers, never read models or change payloads; projection
keeps only namespace/operation metadata. No `UpdateLookup` or raw-content export
is requested. Overflow terminates only the slow subscriber with `ErrOverflow`.

Cursor loss, unknown operations and drop/rename/invalidate terminate the shared
database generation with locally inspectable `ErrResnapshotRequired`. Queued
markers are discarded. Already-admitted rendering may finish. Close/join the old
observation, then explicitly Open a new one for a complete baseline and fresh
transfer state. A generation hint alone does not reset Delta delivery. There is
no automatic resume, durable checkpoint or browser-resubscription guarantee.
Failed generations cannot be replaced before reader and cursor cleanup join.

Stream Close detaches only its subscriber, wakes and joins its active Next, and
never closes the shared cursor. Watcher Close stops admission and cancels **all**
readers before waiting for their cursor cleanup and active consumers. A wait
timeout retains ownership; repeat Close with a fresh budget. Cursor disposal runs
at most once, after iteration ends. A failed disposal remains reported and blocks
generation replacement; repeated joins do not retry a failed driver effect.
Drain Arc observations/resources first, then close/join the watcher, then disconnect
the application-owned client. Never disconnect underneath an unjoined reader.

### C# differences and evidence limits

Compared with C# `MongoDBWatcher.cs`, `MongoCollectionExtensions.cs` and
`MongoDBJoinedObserveBuilder.cs` at `7c1e780`, Go shares database readers but uses
bounded invalidation queues and complete authorized renderer execution. C# retains
resume tokens, replays initial results, supports single/null observations and
incremental membership/pages, and uses unbounded joined-observation channels.
This checkpoint has none of that current/replay, single-result, join or automatic
recovery parity. It retains no query scope for refetch callbacks.

`find_snapshot_test.go`, `watcher_test.go` and `observe_test.go` provide native BSON
detachment, deterministic handoff/fanout/limits, cancellation/continued joining,
terminal recovery and existing-Arc-renderer unit evidence. `watch_driver_test.go`
records ordinary aggregate/getMore/killCursors and borrowed-client usability
through the pinned driver's test-only wire deployment. These tests do **not** prove
live MongoDB watch behavior, missed-update convergence, failover, sharding, real
Chronicle release, browser reconnection or C# current/replay parity. Change streams
require a supported replica set/sharded deployment, database watch permissions,
and the existing primary/majority assumptions. Standalone watch operation is not
supported. Live-provider verification is the next checkpoint, with no CI changes
in this one.

## Evidence and next steps

`ExampleNewRegistry` compiles and executes the driver encoder workflow.
[Literal fixture provenance](https://github.com/Cratis/Arc.Go/blob/develop/integrations/mongodb/testdata/bson/README.md)
records independent BSON-spec/source-derived evidence, not codec-generated
capture. Unit and race tests require no database. Use the independent module
commands in the
[contribution guide](https://github.com/Cratis/Arc.Go/blob/develop/CONTRIBUTING.md).

The compiled `SnapshotHTTPExample` and `examples/httpserver` add complete manual
HTTP hosting with caller-owned authentication, tenant membership and driver
lifetime. Follow the
[MongoDB guide](https://github.com/Cratis/Arc.Go/blob/develop/Documentation/backend/go/mongodb/index.md)
for loopback demo setup, seeding, indexes, query output and security limitations.

The `integration`-tagged `provider_*_integration_test.go` contracts execute actual
Arc registration/rendering/HTTP against a task-owned MongoDB 8.0.15 replica set.
They cover two tenants, forbidden rows, serialized count/find predicates and
coordinates, server sort/window/ties, more than one cursor batch, refetch mutations,
codec round trips, whole-result decode failure, synthetic raw release and decoded
identity collisions, isolated count/find/getMore faults, cancellation/killCursors,
bounded/empty pages, denied no-I/O requests and borrowed-client usability. Outer
operation cleanup failure clears data and paging through the pinned pushed Arc
root `4bd7dca`. Count-to-page overflow uses bounded no-database fakes, not an
unrealistic live dataset. Cancellation preserves Arc's existing empty 500 HTTP
response when its canceled context prevents encoding.

From the repository root, with Docker available, run:

```bash
python3 integrations/mongodb/scripts/replica-set-tests.py \
  --state-file "$PWD/.ai-work/keep/mongodb-provider-local-unique.json"
```

Use a fresh state-file path. The harness pins image digest
`sha256:f4d54619262ae3bc6a0a8efbebcef970b87b8ad70697479a75ce308a6f400158`,
verifies the resolved image ID and owns exactly one fresh-storage container on a
Docker-assigned loopback port. Startup/test/diagnostics-and-cleanup budgets are
90/180/15 seconds. It collects failure logs before removing only its verified
container ID/token, including anonymous volumes; CI also runs exact `always()`
cleanup. Missing prerequisites/URI or unsupported failpoints fail, never skip.
Ordinary module tests require no database. CI separates no-database checks from
Linux Go 1.27 live contracts; publishing stays root-only.

[Arc.Go#22](https://github.com/Cratis/Arc.Go/issues/22) remains Partial: synthetic
release callbacks are not evidence of real Chronicle ciphertext layout, SDK
release or compliance. Sharded/multi-member/failover profiles and live watch evidence remain outside this
checkpoint.
