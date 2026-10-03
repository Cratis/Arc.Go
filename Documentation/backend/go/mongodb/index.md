---
title: MongoDB snapshots and observations
description: Serve authorized MongoDB pages and rerender them on bounded database invalidations.
---

When your read models already live in MongoDB, you can return a typed selection
from an Arc query instead of loading every row and paging in memory. The optional
`github.com/cratis/arc.go/integrations/mongodb` module counts authorized rows and
pushes sorting, skip and limit to MongoDB before Arc publishes the result.

**Source preview, Partial snapshot provider:** there is no tagged module release.
Use Go 1.26 or later and the independently fetchable dependency pins in the module.
The live contracts target MongoDB **8.0.15**, a single-member replica set and the
official Go driver v2.9.1. Count and find are separate reads, not one atomic
snapshot. Database invalidation sources are **Partial**, with manual registration
and terminal recovery rather than automatic resume. Real Chronicle sink layout
and release compatibility are not established.

## Run the manual HTTP example

Prerequisites: a checkout of Arc.Go, Go, `mongosh`, and your own loopback MongoDB
replica set. The example creates no database server. It listens only on
`127.0.0.1:8080`; its environment-configured bearer secret is a local demonstration,
not a production identity-platform adapter. Do not point it at shared data.

Set the connection for your own server. For an externally published single-member
replica set, use its actual loopback port and direct connection:

```bash
export ARC_MONGODB_URI='mongodb://127.0.0.1:27017/?directConnection=true'
export ARC_EXAMPLE_TENANT=Demo
export ARC_EXAMPLE_TOKEN='replace-with-a-random-local-secret-at-least-32-bytes'
```

Create application-owned rows and an index in the example's `Library+Demo`
database. These commands intentionally write to your selected local database:

```bash
mongosh "$ARC_MONGODB_URI" --quiet --eval '
const authors = db.getSiblingDB("Library+Demo").getCollection("Authors");
authors.insertMany([
  {_id: 1, Name: "Ada", Active: true, OwnerID: "reader"},
  {_id: 2, Name: "Grace", Active: true, OwnerID: "reader"},
  {_id: 3, Name: "Hidden", Active: true, OwnerID: "another-reader"}
]);
authors.createIndex({OwnerID: 1, Active: 1, Name: 1, _id: 1});
'
cd integrations/mongodb
GOWORK=off GOTOOLCHAIN=local go run ./examples/httpserver
```

The [complete host](https://github.com/Cratis/Arc.Go/blob/develop/integrations/mongodb/examples/httpserver/main.go)
configures the driver, verifies the local secret, selects the fixed tenant,
checks membership, registers the provider and drains Arc before disconnecting the
borrowed client. The model and namespace query below are a maintained declaration
excerpt from the compiled example:

<!-- mongodb-snippet: Author -->

```go
type Author struct {
    ID     int32  `json:"id" bson:"_id"`
    Name   string `json:"name" bson:"Name"`
    Active bool   `json:"active" bson:"Active"`
}

// AllActive is a namespace query returning a trusted MongoDB predicate.
func (Author) AllActive(context.Context, queries.NoArguments) (mongodb.Find[Author], error) {
    return mongodb.Find[Author]{Filter: bson.D{{Key: "Active", Value: true}}}, nil
}
```

Registration uses `NewCollection[Author]`, a borrowed operation resource holder,
`RegisterRenderer[Find[Author], []Author]` and `RowFilter` on the verified subject's
`OwnerID`. `SnapshotHTTPExample` supplies GET `/authors`; no discovery or generated
MongoDB adapter is implied.

In another terminal with the same token, fetch the second authorized row:

```bash
curl -sS 'http://127.0.0.1:8080/authors?page=1&pageSize=1&sortby=Name&sortDirection=asc' \
  -H "Authorization: Bearer $ARC_EXAMPLE_TOKEN"
```

The 200 query envelope contains `data: [{"id":2,"name":"Grace","active":true}]`
and `paging: {"page":1,"size":1,"totalItems":2,"totalPages":2}`. The hidden row
contributes neither data nor count. Without credentials this route returns 403
and performs no MongoDB operations; invalid credentials are rejected by ingress.
Stop the host with Ctrl-C. To repeat seeding, remove only the three IDs you created
in your own local database, or choose a fresh tenant and matching database; do not
drop a shared database.

## Configure your own collection

Give each persisted field an explicit JSON name. Use `bson` overrides for existing
storage names; absent BSON tags use the exact JSON name. Bindings require an
ordinary nonnullable scalar `_id`, an explicit database, collection and ownership.
See the [BSON storage profile](https://github.com/Cratis/Arc.Go/blob/develop/integrations/mongodb/README.md#storage-profile)
for supported types, null semantics, UUID byte order and temporal precision.

`RowFilter` is mandatory. A missing callback or a nil returned predicate fails
closed; explicit `bson.D{}` intentionally authorizes all rows. Arc membership and
endpoint authorization precede provider operations. Filters are trusted
application BSON, never request-supplied operators. Declared top-level JSON sort
fields map to storage names, with `_id:1` appended for deterministic ties.

Defaults retain at most 1000 rows and 16 MiB raw BSON within a ten-second operation
budget. Oversized pages, retained-size overflow and count-to-page overflow fail,
not truncate. Acquired cursors close with a detached five-second cleanup budget.
Count/find/getMore, decoding, release and scope-cleanup failures retract all data
and paging metadata. The required paging envelope remains present with zeros.
A canceled HTTP request cannot encode a response body; it is not partial success.

Configure least-privilege read credentials and verified TLS for production.
Applications own topology, indexes, read-retry settings and client lifetime.
Primary/majority and simple collation are the configured profile; the driver may
rewrite Primary to primaryPreferred for direct/Single connections. Use
replica-set discovery for topology-aware primary selection. Bounds do not make
unindexed authorized counts cheap.

## Observe an authorized collection

When a page should change with its collection, return invalidation instructions
instead of fetching rows inside a change-stream callback. Arc rechecks membership
and policies before each candidate, then uses the same renderer for fresh row
authorization, count, window, release and interception.

The compiled
[ObservationExample](https://github.com/Cratis/Arc.Go/blob/develop/integrations/mongodb/examples/observation/observation.go)
constructs an application-lifetime watcher and manually registers
`Source[Find[Author]] -> []Author` against the existing renderer. It accepts your
borrowed client and membership authority; verified identity and tenant must reach
Arc before Open. This source declaration is an excerpt from that example, using
its `Author` and the `mongodb`, `observable` and `bson` imports:

<!-- mongodb-observation-snippet: ActiveAuthors -->

```go
// ActiveAuthors returns trusted invalidation instructions, not rendered rows.
// Register this source against the renderer for the same collection binding.
func ActiveAuthors(watcher *mongodb.Watcher, authors *mongodb.Collection[Author]) (observable.Source[mongodb.Find[Author]], error) {
    return mongodb.Observe(watcher, authors, mongodb.Find[Author]{Filter: bson.D{{Key: "Active", Value: true}}})
}
```

Pair the source and renderer with the **same collection binding**. Use per-query
`WithRenderer` when one model has multiple bindings; model type does not identify
a server or database. Opaque provider emissions require manual registration, not
a generated proxy. The example has no HTTP host, credential verification, seed
data or indexes. Check its registration from the module directory:

```bash
GOWORK=off GOTOOLCHAIN=local go test -run ExampleObservationExample -v ./examples/observation
```

It prints `true []observation.Author`. Actual streaming/waiting requires your
supported replica set and database change-stream permissions. Non-wait snapshots
remain pending because this is `Source`, not `CurrentSource`. Waiting or streaming
establishes the database cursor **before** the initial renderer baseline. Inserts,
updates, replacements and deletes invalidate the complete selection, including
filter exits and off-page changes. Mutations during rendering or blocked delivery
remain queued. Quiescent comparison proves convergence, not atomic count/find or
one delivery per historical mutation; equivalent duplicate results are allowed.

Share one watcher per borrowed client. Models and collections sharing a resolved
database share its cursor; distinct clients and tenant databases do not. Default
limits are 32 databases, 1024 subscribers, 64 per logical query and 16 queued
markers. Overflow terminates only the slow subscriber. Each candidate uses a
fresh RowFilter; membership or policy revocation before that candidate prevents
renderer I/O and produces terminal denial. There is no idle-policy polling.

Cursor loss and drop/rename terminate the database generation with locally
inspectable `ErrResnapshotRequired`, not an in-place resume. Clients receive Arc's
safe terminal error. Close the old observation and explicitly Open a new one after
reader/cursor cleanup joins; an unjoined generation rejects admission with
`ErrJoinPending`. Fresh Open establishes a new cursor and full first Delta result,
then subsequent changes. A generation hint alone does not reset delivery state.
There is no live connection migration, durable checkpoint, replay, gap-free
recovery or browser automatic-resubscription guarantee.

Streams retain Open's cancellation channel and copied deadline, not its query
metadata or context values. Cancellation first observed at/after that deadline
reports `context.DeadlineExceeded`; before it, `context.Canceled`. The first
classification stays fixed, so an earlier cancellation observed late cannot
preserve its historical error or custom cause. Next's supplied context still
propagates its actual error. Stream Close releases this state only after joining
active Next work.

Drain `CloseObservations`, then join `Watcher.Close`, then disconnect your borrowed
client. Continue a canceled join wait with a fresh context. A failed cursor
disposal remains an error and blocks generation replacement; repeating Close
does not repeat that failed driver effect. See the
[observation ownership reference](https://github.com/Cratis/Arc.Go/blob/develop/integrations/mongodb/README.md#database-invalidation-sources)
for all bounds and C# differences.

## Chronicle-owned boundaries

`ChronicleOwned` requires `Release`. The callback receives complete copied raw
BSON, including opaque ciphertext and lineage, before protected-field decoding or
Arc interception. It owns actual mapping and release. Return the same row
count/order/identity; errors, partial output, reordered identities and duplicate
decoded identities fail with no publication. Distinct BSON IDs such as integer
`1` and string `"1"` can coerce to one Go ID: this ambiguity fails closed rather
than guessing correspondence. Only approved cleartext sink fields may be filtered
or sorted.

The recording callback tests use synthetic ciphertext and lineage. They prove
boundary enforcement, **not** a real Chronicle ciphertext layout, SDK release call
or compliance guarantee. Do not configure an ordinary decoder as a fake release.

## Provider contracts and limits

From the repository root, run the required live lane with Docker available:

```bash
python3 integrations/mongodb/scripts/replica-set-tests.py \
  --state-file "$PWD/.ai-work/keep/mongodb-provider-local-unique.json"
```

Use a fresh state-file path for each task. The harness owns one fresh-storage
container, pins `mongo:8.0.15` by digest, verifies its image ID, asks Docker for a
loopback port, initiates the replica set and supplies the external direct URI.
It budgets 90 seconds for startup, 180 for tests and 15 for diagnostics/cleanup.
Failure logs precede removal of only the exact container with its ownership token,
including anonymous volumes. Signal cleanup and CI's `always()` fallback use that
same token/ID, never a shared container, database drop, port-owner kill or prune.
Missing Docker, URI or failpoint support fails the required lane rather than
passing through a skip. Ordinary module tests never require a database.

`provider_*_integration_test.go` exercises the actual registered renderer and Arc
HTTP host: two tenants, forbidden rows, multi-batch cursors, serialized count/find
predicate/coordinate/collation equality, server windows, deterministic ties,
bounds, empty/out-of-range pages, no double paging, UUID/concept/null/temporal
storage, decode failure, synthetic release, count/find/getMore failpoints,
cancellation/cursor cleanup and borrowed-client usability. The separate
`observation*_integration_test.go` cases use real ordinary
`Aggregate($changeStream)` cursors through Arc Open/Run, not `Database.Watch` or a
callback refetch. They compare every full candidate with the equivalent authorized
unary query, including initial-render/delivery barriers, filter entry/exit,
off-page mutations, reorder/refill, totals, independent principals/tenants/models,
shared versus distinct clients, fresh row filters and synthetic raw release before
interception. Driver command monitoring checks the single database watch,
metadata-only projection, bounded batches and absence of UpdateLookup/resume.
Revocation denies before render I/O; overflow remains subscriber-local. Live
startup cancellation, canceled cleanup joining, cleanup-budget failure,
CursorNotFound and injected ChangeStreamHistoryLost, explicit new-Open first-full
Delta recovery, drop and rename are covered. Natural small-oplog history expiry
is **unverified**: a failpoint response is not evidence of actual retention expiry.
Concurrent Open/Close/Next and broader limit boundaries also have deterministic
unit coverage. Huge count conversion/overflow cases use bounded no-database driver fakes; the
live lane does not seed billions of rows. The manual example also crosses a real
loopback HTTP listener. Linux Go 1.27 is the required provider CI profile;
ordinary no-database checks keep the existing toolchain/OS matrix.

Follow the [parity map](../../../parity.md) for remaining gaps. Sharded or
multi-member deployments, failover and real Chronicle sink release remain outside
this evidence. Collection joins, single/null observations, CurrentSource/replay,
automatic recovery and opaque-provider generation are deferred.
