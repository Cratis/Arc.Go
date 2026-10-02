---
title: Arc.Go parity
description: Versioned feature-by-feature compatibility with the pinned C# Arc contract.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

## Baseline v1

Arc.Go implements foundation contracts, **not a running Arc server**. Ledger and
fixture format version: **1**. Descriptor format: `metadata.Version == 1`.
Work is tracked in [foundation issue 3](https://github.com/Cratis/Arc.Go/issues/3).

| Profile | Reference | Coverage |
| --- | --- | --- |
| Current C# authority | Arc commit `7c1e78075b737df64f69fddfaae83374f75e3612` | Source-derived foundation fixtures and behavioral tests below |
| Historical Kotlin comparison | C# Arc 22.14.0; Kotlin commit `23c93a3f70008f20d99c28868010b08e3123ea97` | Task-board assertions consulted; paired HTTP suite not run |
| Historical TypeScript comparison | C# Arc 22.45.0; TypeScript commit `cbb421aa1fb6e5f65deec746911ee3046dfb1af6` | Full-envelope expectations consulted; paired HTTP suite not run |

C# paths in the ledger are relative to `Source/DotNET/Arc.Core/` at the authority
revision above. Scalar naming/converter sources additionally pin Fundamentals
`d2accc4a79b6bcf2708213c97093ab5ba6c06381`.
[Fixture provenance](../ContractTests/fixtures/v1/README.md) identifies exact files,
versions and normalization. Fixtures are not live .NET captures.

## Status vocabulary

- **Implemented**: the named, bounded contract has executable regression evidence.
- **Partial**: the stated subset works; identified gaps remain.
- **Not implemented**: no working Go surface, even if the contract is understood.
- **Go-specific**: deliberate construction or behavior differences, described below.

## Feature ledger

| Feature | C# source | Go surface | Status | Behavioral/wire evidence and remaining scope |
| --- | --- | --- | --- | --- |
| Explicit artifact descriptors v1 | `Commands/ICommandHandler.cs`, `Queries/IQueryPerformer.cs` | `metadata.Catalog`, `Command`, `Query`, `TypeName` | Go-specific | `metadata/routes_test.go`; logical identities do not depend on Go package names. Handler/type-schema/auth graphs await their owning slices |
| Stable query identity | `Queries/ModelBound/ModelBoundQueryPerformer.cs` | `Query.Identity` | Implemented | `TestQueryIdentityAndPathPrecedence`; includes model name independently of route |
| Literal route resolution | `Http/EndpointRouteHelper.cs`, `StringExtensions.cs`, endpoint mappers | `metadata.Resolve`, `DefaultOptions` | Implemented | `TestConventionalRoutes`, `TestNamespaceConflictsRestoreNames`; API prefix, skips, acronym/underscore casing, name fallback and GET/QUERY/POST/validate |
| Explicit path precedence | `Commands/CommandRoute.cs`, `Queries/ModelBound/ModelBoundQueryPerformer.cs` | `Command.Path`, `Query.Path`, `ReadModelPath` | Implemented | `TestQueryIdentityAndPathPrecedence`; exact casing/trailing slash; empty method override disables model override |
| Collision/configuration diagnosis | `Queries/QueryEndpointMapper.cs` | `CollisionError`, `Resolve` | Go-specific | `TestCollisionsAreDeterministicAndInspectable`, `TestInvalidMetadataFailsExplicitly`; no first-performer-wins behavior |
| UUID wire value | Guid and concept converter | `concepts.UUID` | Partial | Golden scalar and RFC-order tests; dashed input only, not every Guid.Parse spelling |
| Calendar/duration output | Fundamentals DateOnly/TimeOnly converters; System.Text.Json TimeSpan | `DateOnly`, `TimeOnly`, `TimeSpan` | Implemented | `ContractTests/scalars_test.go`, `concepts/scalars_test.go`; canonical strings, zero values and full signed tick range |
| Calendar input | Same converters | `ParseDateOnly`, `ParseTimeOnly`, `ParseTimeSpan` | Partial | Boundary and failure-atomicity tests; invariant input only, no culture-dependent parsing or DateOnly timestamp coercion |
| Model naming/nulls/numbers | `JsonSerializerOptionsConfiguration.cs` | `serialization.Marshal`, `Unmarshal` | Partial | `serialization/serialization_test.go`; acronym-preserving naming, tags, named primitives, null omission, numeric enums, named nonfinite floats, nested models. Embedded fields, complex dictionary keys, polymorphism and descriptor-generated codecs await later slices |
| Input presence and duplicates | Command and query request readers | `serialization.Optional[T]`, `Some`, `Null` | Go-specific | Presence, duplicate/case-fold and atomic binding tests; unknown properties ignored. HTTP GET/QUERY binding not implemented |
| Command result envelope/flags | `Commands/CommandResult.cs` | `commands.Result[R]`, `Details`, `Success`, `WithResponse` | Implemented | Nine command goldens plus exhaustive status/response-presence tests; no pipeline or automatic exception conversion/redaction |
| Failed scalar response omission | `CommandResult<T>.ClearResponse` | `commands.NewResult` | Go-specific | `TestStatusPrecedenceAndFailedResponseOmission`; removes failed zero-valued responses rather than emitting C# default(T) |
| Query result envelope/flags | `Queries/QueryResult.cs` | `queries.Result[T]`, `Details`, `Success`, `NotReady` | Implemented | Eight query goldens, readiness and precedence tests; ready null is not pending |
| Paging response metadata | `Queries/PagingInfo.cs` | `queries.PagingInfo` | Partial | `TestPagingInfo` and page golden cover in-range semantics; out-of-range page counts return MinInt32 deterministically, without a runtime-specific C# overflow claim. Request readers, validators, sorting and actual paging not implemented |
| Change-set envelope | `Queries/ChangeSet.cs` | `queries.ChangeSet` | Implemented | Delta golden, independent array copies, item encoding failures; no diff computation or delivery protocol |
| Validation findings | `Validation/ValidationResult*.cs` | `validation.Result`, `Severity`, `Reason` | Implemented | Four standalone goldens plus findings in results; open reasons, nil state omission and required members array |
| Standard status precedence | `Http/EndpointRouteHelper.cs` | Result `StatusCode` methods | Implemented | All constructed flag combinations; ingress 401, QUERY reader 400 and wait 408 remain transport work |
| Command/query execution | Command/query pipelines | None | Not implemented | No handlers, filters, validators, authorization or execution scopes |
| Contexts and lifecycle | Operation/correlation contexts and scopes | UUID fields only | Not implemented | No fabricated ambient context or service registry; foundation APIs are synchronous values |
| HTTP hosting/discovery | Endpoint mappers and hosting | None | Not implemented | No endpoints registered; 404/405, HEAD, redirects and request limits await hosting |
| Observable queries | Observable handlers/demultiplexer | Change-set DTO only | Not implemented | No SSE, WebSocket, revisions, subscriptions or snapshot waits |
| Proxy generation/OpenAPI | ProxyGenerator/OpenAPI | Route descriptors only | Not implemented | No generated clients, schema or browser-runtime conformance |
| Chronicle integration | Arc Chronicle integration | None | Not implemented | Future adapter uses Chronicle.Go participant/owner transactions; source IDs remain arbitrary strings, distinct from HTTP UUIDs |
| Full product compatibility | All C# surfaces | None | Not implemented | Foundation tests do not certify whole-product or HTTP conformance |

## Intentional differences and migration

- Supply explicit stable namespaces/type names instead of CLR discovery. Moving Go
  source does not change routes or subscription names. Full descriptors will grow
  with pipeline/generator work; v1 does not pretend to describe unsupported schema.
- Resolve rejects duplicate identities and case-insensitive method/path collisions
  rather than silently skipping a performer. Give each conflicting artifact an
  explicit path. Wildcards, relative/escaped paths, whitespace, repeated explicit
  slashes and dot segments fail instead of adopting host-dependent behavior.
- A zero result fails closed (unauthorized); use success constructors or explicitly
  set `Authorized` and query `Ready`. C# default construction is authorized/ready.
- A failed command never carries a response, even for a non-nullable scalar.
  This avoids C# `ClearResponse` retaining `default(T)` under null-only omission.
- Binding rejects duplicate declared names, including case variants, rather than
  last-value-wins. Send each property once; unknown fields still do not fail.
- Use invariant calendar text and dashed UUIDs. Go deliberately does not guess a
  server culture. Input forms outside those documented codecs must be normalized
  by the caller or a custom codec.
- Paging totals outside the int32 page-count range return MinInt32; avoid such
  metadata. C# uses an unchecked conversion whose overflow behavior is not certified
  by these fixtures. In-range calculations follow its double-precision ceiling.
- Arc's naming policy preserves leading acronyms (`ID`, `URLValue`). Add `json:"id"`
  explicitly where the wire name should be lowercase. Plain `encoding/json` does
  not implement Arc's model policy; use `serialization.Marshal`/`Unmarshal`.
- Foundation serialization is a bounded explicit-registration fallback, not the
  future generated descriptor codec. Custom JSON codecs own their behavior.
  Arbitrary Unicode escaping/property order is not a byte-identity promise; literal
  scalar fixtures and canonical envelope bytes are the tested boundary.

## Next slices

ARC-GO-04 adds immutable execution metadata/scopes; 05 adds validation and security;
06/07 add command/query execution; 08 adds hosting. Historical paired HTTP and real
browser consumers belong after those boundaries exist. Chronicle integration must
use Chronicle.Go's transaction participant/owner contract, not a second Arc UoW.
