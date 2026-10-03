---
title: MongoDB snapshots
description: Serve bounded, authorized MongoDB pages through a manually registered Go query and HTTP host.
---

When your read models already live in MongoDB, you can return a typed selection
from an Arc query instead of loading every row and paging in memory. The optional
`github.com/cratis/arc.go/integrations/mongodb` module counts authorized rows and
pushes sorting, skip and limit to MongoDB before Arc publishes the result.

**Source preview, Partial snapshot provider:** there is no tagged module release.
Use Go 1.26 or later and the independently fetchable dependency pins in the module.
The live contracts target MongoDB **8.0.15**, a single-member replica set and the
official Go driver v2.9.1. Count and find are separate reads, not one atomic
snapshot. Watches/change streams are **Not implemented**. Real Chronicle sink
layout and release compatibility are not established.

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
cancellation/cursor cleanup and borrowed-client usability. Insert/update/delete,
filter entry/exit, reordering and refill are **refetch tests**, not observation.
Huge count conversion/overflow cases use bounded no-database driver fakes; the
live lane does not seed billions of rows. The manual example also crosses a real
loopback HTTP listener. Linux Go 1.27 is the required provider CI profile;
ordinary no-database checks keep the existing toolchain/OS matrix.

Follow the [parity map](../../../parity.md) for remaining gaps. Sharded or
multi-member deployments, failover and real Chronicle sink release remain outside
this evidence. Watches need a separate lifecycle/resume/authorization design.
