---
title: Generate TypeScript proxies
description: Publish supported models, commands, snapshot queries, and observable queries alongside their Go adapters, and check owned output safely.
---

Keep your frontend contract aligned with your Go declarations instead of copying
request and result types. The experimental `arc-gen` application profile generates
models, numeric enums, commands, **snapshot queries**, and declared **observable
queries** together with their Go adapters. Compatibility is Partial: the tested
client versions are Arc/Arc.React 22.48.2 and Fundamentals 7.22.0. Proxy-only
output is unsupported. Generated observable hooks have bounded mounted Node
evidence; browser and complete React parity remain unverified.

## Select the application profile

First [install the pinned source tool and declare artifacts](index.md). Select the
complete application artifact scope, not a composition package that refers to
generated symbols. Handwritten artifact source must compile without those symbols.
Use fresh output destinations: adapters created by adapter-only generation do not
have mixed-output ownership and cannot be adopted automatically.

Save this illustrative profile as `arc-gen.json` in an application whose artifact
packages live under `features/`:

```json
{
  "formatVersion": 1,
  "name": "shop-frontend",
  "defaultNamespace": "Shop",
  "routes": { "routePrefix": "/api", "segmentsToSkip": 1 },
  "typescript": {
    "out": "web/src/api",
    "clientVersion": "22.48.2",
    "proxyFileSuffix": false
  }
}
```

Static namespaces supply frontend identities. Add `packageNamespaces` mappings
when reachable dependency models have no source namespace. Dependency models can
receive TypeScript output, but dependency packages never receive Go adapters.

From the application module:

```bash
GOWORK=off GOTOOLCHAIN=local arc-gen -config arc-gen.json ./features/...
GOWORK=off GOTOOLCHAIN=local arc-gen -config arc-gen.json -check ./features/...
```

Successful generation reports the adapter and TypeScript file counts, profile,
and contract fingerprint. The output root resolves relative to your module and
contains a manifest, type files, and sorted per-directory barrels. Commit generated
files and the manifest with their inputs. `-check` compares inventory and bytes
without writing, deleting, creating directories, or repairing interrupted output.

Call each selected package's generated `RegisterArtifacts(builder)` before
`Build`, and configure the builder with the profile's route options. Generated
adapters register endpoint expectations through `Builder.ExpectGeneratedEndpoints`;
route disagreement, including drift caused by additional manual artifacts, fails
`Build`. Frontend deployment base paths remain runtime settings, not generator
route prefixes. Roles on proxies are hints, never server authorization.

## Supported shapes and explicit limits

The generator analyzes the whole selected catalog, resolves routes through
`metadata.Resolve`, and renders the complete plan before publication. Discovery-
hidden and frontend-excluded artifacts still participate in route resolution.
Application initialization, codecs, validators, and business methods never execute
for discovery.

- Models use class-based field metadata and the shared scalar/concept classifier.
  Nested models, numeric enums, supported declared derivatives, and rich Guid/date
  constructors retain their wire identities.
- Commands support typed scalar/model/list responses and the portable rules
  `notNull`, `notEmpty`, length bounds, and numeric comparison bounds. Mixed
  generation registers `validation.NewPortable` on the server too; independent
  server rejection is not replaced by client validation.
- Model-owned single, pointer, slice, array, and `queries.Page` snapshots become
  model or model-array payloads. Plain lists do not acquire synthetic paging.
  GET/HTTP QUERY preference must agree with the exposed endpoint methods.
- Observable queries declare `observable.Source[O]`, `CurrentSource[O]`,
  `*State[O]`, or `*Subject[O]`, including aliases. Their emissions use the same
  owning-model single/pointer/list/array shapes, value `queries.Page[M]`, or value
  `queries.ObservedCollection[M]`. Source wrappers and change hints are not client
  data. Generated `ObservableQueryFor` classes expose subscription, snapshot
  perform, sorting, paging, and hook helpers through the pinned client runtime;
  they do not generate a transport or delta algorithm.
- Observable collection proxies accept either one unambiguous direct Go `ID`/`Id`
  serialized as `id`, with a supported nonnullable scalar and no omission tag,
  or no identity at all (C# JSON fallback). A serialized `id` without `ID`/`Id`,
  and competing, JSON-hidden or embedded conventional members, fail before
  publication. An identity tag is not a custom client delta extractor.
- Primitive query defaults validate the original Go grammar and target width,
  but remain server defaults rather than initialized client values. Nullable
  collection elements and rich defaults are unsupported. Snapshot query string
  rules support `notNull`, `notEmpty`, `minLength`, `maxLength`, and `length`, with
  matching server and client validators. Observable-query validation and unproved
  rule shapes are rejected. Typed server validators and named policies use
  [validator and policy directives](validators-and-policies.md); these do not
  translate arbitrary application code into client validation.
- Sorting helpers use declared sortable result-wire fields, not query arguments,
  per [Arc#2998](https://github.com/Cratis/Arc/issues/2998). They do not add backend
  provider paging or sorting.

Unsupported interfaces, opaque codecs, dynamic responses, rule forms, derived
providers, rich dictionaries, eager constructor cycles, unsafe names/layouts,
and unsupported source/channel/provider results fail instead of producing `any`
or a partial successful plan. Custom opaque `Source[Find]` MongoDB generation is
unsupported until explicit emission metadata exists, even when manual runtime
registration works. This subset does not cover every C# rich nullable or provider
layout. Custom imports, interface output, source grouping, and
library mode are unsupported. `-emit-go=false` fails because proxy-only runtime
contract verification is not implemented.

Query keys containing regex metacharacters are rejected for the pinned Arc helper
([Arc#3014](https://github.com/Cratis/Arc/issues/3014)). JavaScript numeric and
temporal precision limits remain. The pinned Fundamentals serializer rejects
null-valued field serialization; correct null hydration is not a claimed null
round-trip fix ([Fundamentals#1148](https://github.com/Cratis/Fundamentals/issues/1148)).
Enumerable command response annotations retain the pinned C# element generic even
though runtime responses are arrays. Derived classes must be loaded, normally
through their generated directory barrel, for runtime discriminator selection.

## Ownership and recovery

TypeScript-enabled publication uses `.arc-gen-manifest.json` to bind profile owner,
selected package/build-tag scope, relative paths, fingerprint, and content hashes.
Use separate roots for different scopes. Existing files must match both manifest
ownership and hashes/markers. Even byte-identical marked files, barrels, and legacy
adapters are rejected without ownership. Preserve and move reviewed files or use
fresh consumer/output roots; there is no migration bypass.

Preflight checks the complete physical path graph, including both roots, metadata
files, active outputs, stale entries, and pending recovery. File/directory, case,
symlink, traversal, and unsafe device-name collisions fail before mutation.
Stale deletion requires matching ownership, marker, and hash. User files and
directories are never blanket-deleted. Unchanged files retain their modification
times; missing active files can be regenerated.

Publication is atomic per file, **not** a transaction across directories. The
`.arc-gen-pending.json` journal records old/new manifests and changed bytes before
mutation. A write, rename, delete, or manifest failure returns failure and preserves
that journal. Fix the cause and rerun the original invocation with unchanged inputs
to validate live bytes, roll back the interrupted set, and publish the full plan.

Intervening edits stop recovery without overwriting evidence; an existing empty
file is not equivalent to absence. Preserve the journal and edits, then restore
the recorded owned bytes before rerunning. Never delete the journal to hide a
failed publication. Check mode rejects pending recovery without repairing it.
Use a trusted single-writer workspace. Recovery covers process interruption, not
power-loss durability.

## Executable compatibility evidence

The production fixture runs the actual CLI, compiles the generated Go adapters
against a fetchable runtime pin, and strictly compiles emitted TypeScript against
locked real declarations. Its joined loopback host exercises command validation
and execution, GET/HTTP QUERY snapshots, complete envelopes, rich hydration, and
independent server rejection. Publication tests cover failed writes/renames/deletes,
recovery edits, empty-versus-absent files, full path preflight, and unmanifested
ownership rejection.

Observable production tests generate mixed families and verify exact output with
`-check`; independent Go consumers exercise lazy factories, argument/authorization
admission, current/pending snapshots, failures, cancellation, and join-before-
disposal. Strict TypeScript compilation covers observable hook signatures with
both decorator modes, not mounted hooks.

The locked Node lane executes an actual production-generated model-bound query
using untouched default WebSocket hub + Delta settings: shared socket, date/model
hydration, exact change sets and callback counts, argument replacement, independent
cancellation, terminal Unauthorized, and joined host shutdown with cumulative
source counters. Its final collection uses an independent test-consumer reducer,
not React. The existing manual lane separately executes the wider transport
matrix. A third lane mounts generated observable hooks using real providers with
explicit WebSocket hub/Delta: rich collection reconstruction, argument replacement,
cache sharing/reuse and joined source/resource cleanup. It does not use the full
Arc wrapper or prove browser/DOM/StrictMode, suspense, reconnect, 30-second expiry,
server-side paging or setter correctness. Same-key setter retention characterizes
[Arc issue 2869](https://github.com/Cratis/Arc/issues/2869) at 22.48.2, not a fix.
Paired .NET hosts and full frontend parity remain unverified. The [observable fixture](../../../../ContractTests/observables/README.md)
and [parity map](../../../parity.md) record the exact limits; the
[CLI reference](reference.md) lists invocation options.
