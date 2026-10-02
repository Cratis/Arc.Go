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
| Explicit artifact descriptors v1 | `Commands/ICommandHandler.cs`, `Queries/IQueryPerformer.cs` | `metadata.Catalog`, `Command`, `Query`, `TypeName` | Go-specific | `metadata/routes_test.go`; logical identities do not depend on Go package names. Handler/type-schema graphs await their owning slices; optional authorization metadata extends format 1, with keyed literals required for v0 struct compatibility |
| Stable query identity | `Queries/ModelBound/ModelBoundQueryPerformer.cs` | `Query.Identity` | Implemented | `TestQueryIdentityAndPathPrecedence`; includes model name independently of route |
| Literal route resolution | `Http/EndpointRouteHelper.cs`, `StringExtensions.cs`, endpoint mappers | `metadata.Resolve`, `DefaultOptions` | Implemented | `TestConventionalRoutes`, `TestNamespaceConflictsRestoreNames`; API prefix, skips, acronym/underscore casing, name fallback and GET/QUERY/POST/validate |
| Explicit path precedence | `Commands/CommandRoute.cs`, `Queries/ModelBound/ModelBoundQueryPerformer.cs` | `Command.Path`, `Query.Path`, `ReadModelPath` | Implemented | `TestQueryIdentityAndPathPrecedence`, `TestDerivedCommandPathsAreValidated`; exact casing and query trailing slash; command paths must also yield a valid `/validate` path; empty method override disables model override |
| Collision/configuration diagnosis | `Queries/QueryEndpointMapper.cs` | `CollisionError`, `Resolve` | Go-specific | `TestCollisionsAreDeterministicAndInspectable`, `TestInvalidMetadataFailsExplicitly`; no first-performer-wins behavior |
| UUID wire value | Guid and concept converter | `concepts.UUID`, alias of `github.com/cratis/fundamentals.go/concepts.UUID` at `v0.0.0-20261002203106-9d83ef2578a5` | Partial | `ContractTests/scalars_test.go`, `concepts/scalars_test.go`, `concepts/aliases_test.go`; unchanged golden bytes, RFC order and shared-type identity/round trips; dashed input only, not every Guid.Parse spelling |
| Calendar/duration output | Fundamentals DateOnly/TimeOnly converters; System.Text.Json TimeSpan | `DateOnly`, `TimeOnly`, `TimeSpan`, aliases provided by `github.com/cratis/fundamentals.go/concepts` at `v0.0.0-20261002203106-9d83ef2578a5` | Implemented | `ContractTests/scalars_test.go`, `concepts/scalars_test.go`, `concepts/aliases_test.go`; unchanged canonical strings, zero values, full signed tick range and shared-type identity/round trips |
| Calendar input | Same converters | `ParseDateOnly`, `ParseTimeOnly`, `ParseTimeSpan`, forwarding to `github.com/cratis/fundamentals.go/concepts` at `v0.0.0-20261002203106-9d83ef2578a5` | Partial | Boundary and failure-atomicity tests; invariant input only, no culture-dependent parsing or DateOnly timestamp coercion |
| DateTime/DateTimeOffset | `JsonSerializerOptionsConfiguration.cs` (default System.Text.Json timestamp codecs) | `time.Time` through `serialization.Marshal`, `Unmarshal` | Partial | `TestTimestampWirePolicy` pins RFC3339Nano output: UTC `Z`, up to nine fractional digits, explicit offsets. C# DateTimeOffset emits `+00:00` for UTC and at most seven digits; DateTime suffix depends on kind. Not all C# timestamp input forms are accepted; no DateTimeOffset-specific codec |
| ConceptAs<T> → Concept[T] recognition | Fundamentals `Concepts/ConceptAs.cs`, `Json/ConceptAsJsonConverter.cs` at `d2accc4`; Arc `JsonSerializerOptionsConfiguration.cs` at `7c1e780` | `concepts.Concept[T]` alias; `serialization.ValidateType`, cached shared `Underlying` metadata | Partial | `ContractTests/concepts_test.go`: UUID forwarding, string/int, pointers, slices/map values, inspectable marker-only/nested rejection before encoding/binding, metadata-only concurrent plans, encoded fields checked with `CheckJSON`. Codecs remain application-owned; HTTP GET/QUERY text binding, decimal/enum concepts and generated schema/proxy recognition are not implemented |
| Model naming/nulls/numbers | `JsonSerializerOptionsConfiguration.cs` | `serialization.Marshal`, `Unmarshal` | Partial | `serialization/serialization_test.go`, `codec_regression_test.go`; acronym-preserving naming, exact case-sensitive binding, tags, null omission, numeric enums, nonfinite floats, JSON/text codecs, embedded-field promotion and standard Go omission tags. Complex dictionary keys, polymorphism and descriptor-generated codecs await later slices |
| Input presence and duplicates | `ArcOptions.cs`, `JsonSerializerOptionsConfiguration.cs`, command and query request readers | `serialization.Optional[T]`, `Some`, `Null` | Go-specific | `TestExactWireNames`, `TestNullStringPolicyAndOptionalOutput`, presence and atomic-binding tests; unknown properties including case variants ignored, exact duplicate declared names rejected. Present-null Optionals emit null; plain Go strings reject null unlike C# strings. HTTP GET/QUERY binding not implemented |
| Framework recursion safety | `JsonSerializerOptionsConfiguration.cs` (System.Text.Json traversal) | Model and envelope encoders | Go-specific | `TestFrameworkCyclesAndNestingUseSharedBudget`; one 64-level budget across validation State, command responses, query data/findings/change sets and Optional values. Application custom codecs own their recursion safety |
| Command result envelope/flags | `Commands/CommandResult.cs` | `commands.Result[R]`, `Details`, `Success`, `WithResponse` | Implemented | Nine command goldens plus exhaustive status/response-presence tests; no pipeline or automatic exception conversion/redaction |
| Failed scalar response omission | `CommandResult<T>.ClearResponse` | `commands.NewResult` | Go-specific | `TestStatusPrecedenceAndFailedResponseOmission`; removes failed zero-valued responses rather than emitting C# default(T) |
| Query result envelope/flags | `Queries/QueryResult.cs` | `queries.Result[T]`, `Details`, `Success`, `NotReady` | Implemented | Eight query goldens, readiness and precedence tests; ready null is not pending |
| Paging response metadata | `Queries/PagingInfo.cs` | `queries.PagingInfo` | Partial | `TestPagingInfo` and page golden cover in-range semantics; out-of-range page counts return MinInt32 deterministically, without a runtime-specific C# overflow claim. Request readers, validators, sorting and actual paging not implemented |
| Change-set envelope | `Queries/ChangeSet.cs` | `queries.ChangeSet` | Implemented | Delta golden, independent array copies, item encoding failures; no diff computation or delivery protocol |
| Validation findings | `Validation/ValidationResult*.cs` | `validation.Result`, `Severity`, `Reason` | Implemented | Four standalone goldens plus findings in results; open reasons, nil state omission and required members array |
| Standard status precedence | `Http/EndpointRouteHelper.cs` | Result `StatusCode` methods | Implemented | All constructed flag combinations; ingress 401, QUERY reader 400 and wait 408 remain transport work |
| Command/query execution | Command/query pipelines | None | Not implemented | No handlers, filters or pipeline integration; validators and authorization foundations below are not pipeline enforcement |
| Principal/context | `Authorization/CurrentPrincipalAccessor.cs` (`7c1e780`) | `identity.Principal`, `WithPrincipal`, `PrincipalFrom` | Go-specific | `identity/TestPrincipalSnapshot`, `TestPrincipalContexts`; immutable explicit contexts replace ambient accessors, preserving explicit anonymous shadowing; no authentication or authorization |
| System actor | `Authorization/SystemPrincipal.cs` (`7c1e780`) | `identity.System` | Implemented | `identity/TestSystemRoles`; C# subject/name/role claims, supplied roles only, no privileged bypass |
| Tenant context | `Tenancy/TenantIdAccessor.cs` (`7c1e780`) | `tenancy.WithTenant`, `TenantFrom` | Go-specific | `tenancy/TestTenantContexts`; nested/concurrent explicit contexts replace disposable ambient overrides; NotSet shadows parents |
| Tenant sentinel semantics | `Tenancy/TenantId.cs` (`7c1e780`) | `tenancy.ID`, `ParseID`, `Default` | Partial | `tenancy/TestTenantSentinelsAndCodecs`, `FuzzTenantText`, `FuzzTenantJSON`; zero internally represents `[NotSet]`, named Default remains distinct; Go maps `""` to NotSet whereas C# keeps `""` as a distinct tenant. Go also rejects controls, invalid UTF-8 and surrounding whitespace and accepts JSON strings only |
| Correlation | `Execution/CorrelationIdResolver.cs` (`7c1e780`) | `correlation.ID`, `Parse`, `Normalize`, `Resolve`; `WithID`/`FromContext` delegate to Fundamentals.Go at `66acfed` | Partial | `correlation/TestCorrelationParsingAndReplacement`, `TestCorrelationContextPrecedence`, `FuzzCorrelationParse`, `TestSharedCorrelationContext`, `TestSharedCorrelationNilContextPanics`; UUID alias, bidirectional shared context, read-only access, zero shadowing, cancellation and nil-context semantics unchanged; dashed input subset, no other Guid spellings |
| Receipt metadata | `OperationContextScope.cs` (`7c1e780`) | `execution.WithReceivedAt`, `ReceivedAt`, `Capture`, `NewContext` | Go-specific | `execution/TestReceiptNormalizationAndOverrides`, `TestExplicitMetadata`; UTC value contexts replace ambient leases; pipeline forwarding remains 06/08 |
| Services/scopes | `HostBuilderExtensions.cs` (`7c1e780`) | `services.Registry`, `Provider`, `Scope`, `Resolve`, `execution.Run` | Go-specific | `services/scope_test.go`, `concurrency_test.go`, `cleanup_test.go`, `provider_concurrency_test.go`, `execution/background_test.go`; explicit exact-type registration, isolated metadata, owned reverse cleanup, synchronous fresh background scopes; `TestCreatorCancellationDoesNotFailLiveWaiter`, `TestFailedValueCleanupHasIndependentBoundedContext` pin waiter retry on creator cancellation and synchronous 30s independent failed-value cleanup; no convention DI or implicit command completion |
| Startup graph checks | `ArcApplicationBuilder.cs` (`7c1e780`) | `services.Registry.Build` | Go-specific | `services/TestBuildDoesNotInvokeFactoriesAndFailureIsEditable`, `TestDependencyGraphs`; stronger declared-edge validation in every environment without constructing services; hidden closure captures cannot be checked |
| Typed invocation/failure | `Validation/ValidatorInvoker.cs`, `IValidationFailure.cs` (`7c1e780`) | `validation.Validator[T]`, `Invoke`, `Failure`, `Reject` | Partial | `validation/TestInvokeCopiesAndPreservesRejection`, `TestInvokeRedactsAndPreservesCancellation`; synchronous typed callbacks, wrapped rejection preservation, redacted infrastructure failures and cancellation; no FluentValidation discovery, graph traversal, declarative rule IR or generator projection |
| Command severity policy | `Commands/CommandValidationResults.cs` (`7c1e780`) | `validation.Policy`, `NewPolicy`, `Filter` | Implemented | `validation/TestSeverityMatrix`; default Error-only, strict allowances, inclusive floors, Unknown blocking with floors; unsupported Go numeric findings conservatively block |
| Header allowance | `Commands/CommandEndpointMapper.cs` (`7c1e780`) | `validation.AllowedFromHeader` | Go-specific | `validation/TestAllowedHeader`, `FuzzAllowedFromHeader`; one supported signed decimal int32 only; untrusted Error capped at Warning, unsupported enum bypass closed; caller must use default policy for ignored input; no HTTP mapping yet |
| Authentication chain | `Authentication/Authentication.cs`, `AuthenticationResult.cs` (`7c1e780`) | `authentication.Chain`, `Handler`, `HostPrincipal` | Implemented | `authentication/TestTerminalOrderAndCopies`, `TestInvalidAndCancellation`, `TestTrustedHostAndConcurrentContexts`; terminal success/failure and no rescue, copied handlers and trusted typed host identity only; authenticated result requires an authenticated principal, unlike C#'s nonnull-only result check; `TestAuthenticationClonesRequestContextHeadersAndURL` pins caller-isolated context/headers/URL; Body is shared and handlers must not read it; no auto credential/header/cookie adapter |
| Authentication ingress | `Http/AuthenticationMiddleware.cs` (`7c1e780`) | Chain result contract; no middleware | Not implemented | Future hosting deliberately maps explicit credential failure to 401 including public operations, anonymous exhaustion to authorization, ordinary denial to 403; differs from C# blanket credential requirement and anonymous failure exemption; no HTTP evidence yet |
| Declaration compilation | `Authorization/AuthorizationDeclarations.cs` (`7c1e780`) | `metadata.Authorization`, `authorization.Registry.Build` | Partial | `metadata/TestAuthorizationDescriptorRoundTripAndRoutes`, `authorization/TestBuildValidationAndFreeze`, `TestPrecedenceAndFallback`, `TestRolesAndPolicyOrdering`; all levels validated, method replacement, role OR/requirement AND and copied graph; frozen explicit fallback replaces dynamic discovery; `TestReadModelDeclarationsMustAgree` rejects conflicting declarations across queries of one read model (including nil vs explicit); named schemes rejected |
| Native policies | `Authorization/ArcAuthorizationPolicyRuntime.cs` (`7c1e780`) | Direct `Registry.Register`, `PolicyFunc` | Partial | `authorization/TestGuestConjunction`, `TestPolicyFailureCancellationAndConcurrency`; guest opt-in requires policy-only conjunction, errors fail closed; borrowed concurrently-callable values/closures are primary. Service-resolved `RegisterPolicy[P]` is deferred under the container-design hold; Build has no provider parameter |
| Staged evaluation | `Authorization/AuthorizationEvaluation.cs` (`7c1e780`) | `Evaluator.Prepare`, `Prepared.Evaluate(ctx, resource)`, `Check` | Partial | `authorization/TestPreparedSecurityAndZero`, `TestRecheckAfterPolicyCallback`, `TestPolicyPanicRemainsBoundaryOwned`; principal/tenant continuity, no policy invocation at Prepare or after role denial, cancellation and fresh concurrent tenant evaluations. `TestGuestPolicyReceivesEmptyPrincipalAndRetainsCallerContinuity`, `TestGuestIdentityChangeDuringPolicyInvalidatesVerdict` pin empty anonymous policy input with original-caller continuity; `TestUnknownRuntimeTargetIsRedactedAndNotConfiguration` pins non-disclosing runtime lookup errors distinct from configuration errors. Container-neutral: provider/scope ownership checks deferred; pipeline integration remains 06/07 |
| Tenant selectors/membership | `Tenancy/SubdomainTenantIdResolver.cs`, `TenantHost.cs`, Header/Query/Claim/Fixed resolvers (`7c1e780`) | `tenancy.NewResolver`, `ResolverFunc`, `Membership`, `Require` | Partial | `tenancy/TestSelectorDefaultsAndInputs`, `TestSubdomainHostMatrix`, `TestInvalidOptionsAndMembershipSeparation`, `FuzzResolveHost`; ASCII/punycode only, Unicode explicitly rejected rather than IDNA-normalized; ambiguous header/query values rejected, malformed query encoding fails, DNS/port lengths validated; optional independent membership callback is a Go-specific seam, not a C# membership catalog; `TestClaimSelectorIgnoresUnauthenticatedIdentity` requires authentication for claim tenancy, stricter than C# `ClaimTenantIdResolver.cs`, which reads a claim without checking authentication; no development override or routing |
| Identity details/view | `Identity/IdentityProvider.cs`, `IdentityProviderContext.cs`, `IProvideIdentityDetails.cs` (`7c1e780`) | `identity.DetailsProvider[T]`, `ProvideDetails`, `View[T]` | Partial | `identity/TestDetailsFreshSnapshotAndNonAuthority`, `TestDetailsDenialErrorsAndCancellation`, `TestViewNilDetailsAndBoundedRecursion`; anonymous suppression, fresh callback, safe view with trusted roles, copied claims, unwrapped details and bounded wire encoding. Explicit adapter subject instead of C# sub-claim inference; display fallbacks only; no forwarded-name fallback, endpoint, schema or provider discovery/registration |
| Security seam composition | Same authentication/authorization/validation/tenancy/identity sources (`7c1e780`) | Explicit foundation composition | Go-specific | `ContractTests/TestSecuritySeamsRemainIndependent`; failed credentials terminal, public declarations do not bypass membership, nonblocking warnings do not imply authorization, malicious details remain display-only; no HTTP or pipeline conformance claim |
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
  Derived paths are also checked: root and trailing-slash command paths are rejected
  because C#'s literal `/validate` concatenation yields repeated slashes. Use
  `/create`, not `/create/`; configure a non-root conventional command route.
- A zero result fails closed (unauthorized); use success constructors or explicitly
  set `Authorized` and query `Ready`. C# default construction is authorized/ready.
- A failed command never carries a response, even for a non-nullable scalar.
  This avoids C# `ClearResponse` retaining `default(T)` under null-only omission.
- Binding is case-sensitive like C# Arc's default body serializer; use the exact
  wire name. Only exact repeats of declared names are rejected rather than using
  C#'s last-value-wins behavior. Case variants are unknown fields and do not fail.
- Present-null Optional model properties are written as null to preserve presence
  through a round trip. C# `WhenWritingNull` omits ordinary null properties instead.
  Consumers must handle explicit null; use missing Optionals when omission is wanted.
- Claim tenancy ignores unauthenticated principals and returns NotSet even if they
  carry tenant claims. C# `ClaimTenantIdResolver.cs` at `7c1e780` reads claims
  without checking authentication; Arc.Go requires an authenticated adapter before
  treating a claim as a selector. Membership remains a separate check.
- Plain Go strings reject null, unlike C# reference-type strings under Arc's default
  options (nullable-annotation enforcement is not enabled). Use `*string` or
  `Optional[string]` to accept null without confusing it with an empty string.
- `time.Time` retains Go's RFC3339Nano JSON codec: UTC `Z`, explicit offsets and up
  to nine fractional digits. C# DateTimeOffset uses `+00:00` at UTC and tick
  precision (at most seven digits); C# DateTime also has kind-dependent suffixes.
  Normalize to common precision/forms or provide a codec for exact DateTimeOffset
  spelling; this slice does not implement the full C# timestamp input grammar.
- Arc.Go's UUID and calendar/duration scalars alias Fundamentals.Go's shared
  types; existing imports, constructors, parsers and wire formats are unchanged.
  `Concept[T]` is the shared interface declaration, recognized through cached
  `Underlying` metadata without executing application methods. Supply all four
  text/JSON codecs; marker-only and nested concepts fail preflight with inspectable
  Fundamentals errors. Unlike C# inheritance and converter-created instances, Go
  uses explicit methods and constructors. HTTP text binding and generated schema
  metadata remain future slices.
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
  future generated descriptor codec. Exported embedded fields use standard Go
  promotion; nil unexported embedded pointers cannot be allocated during binding.
  Go omission tags honor `IsZero` and standard empty-kind rules. JSON/text codecs
  own their behavior; pointer-receiver marshalers require addressable values.
  Nil model slices/maps/pointers are omitted like C# null properties; initialize
  slices/maps when an empty collection must be present. Framework envelope arrays
  are separately normalized to `[]`.
  Arbitrary Unicode escaping/property order is not a byte-identity promise; literal
  scalar fixtures and canonical envelope bytes are the tested boundary.

## Next slices

ARC-GO-04 provides immutable execution metadata and explicit service scopes;
05 provides typed validation and container-neutral security foundations; 06/07 add
command/query execution; 08 adds hosting. Service-resolved authorization remains
deferred while container integration is reconsidered. Historical paired HTTP and real
browser consumers belong after those boundaries exist. Chronicle integration must
use Chronicle.Go's transaction participant/owner contract, not a second Arc UoW.
