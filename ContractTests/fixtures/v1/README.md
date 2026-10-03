# Foundation contract fixtures v1

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

These fixtures are **source-derived expectations**, not captured responses from a
running .NET host. They are deliberately independent of the Go implementation.
Do not regenerate them from Go output. A change requires checking the pinned
reference and updating the parity map.

## Authority and provenance

Primary: [Cratis/Arc at 7c1e78075b737df64f69fddfaae83374f75e3612](https://github.com/Cratis/Arc/tree/7c1e78075b737df64f69fddfaae83374f75e3612).
Paths below are under `Source/DotNET/Arc.Core/` at that revision:

| Fixture | Source of fields and semantics |
| --- | --- |
| `envelopes.json`, `command-*` | `Commands/CommandResult.cs`, `JsonSerializerOptionsConfiguration.cs` |
| `envelopes.json`, `query-*` | `Queries/QueryResult.cs`, `Queries/PagingInfo.cs`, `Queries/ChangeSet.cs` |
| `envelopes.json`, `validation-*` | `Validation/ValidationResult.cs`, `Validation/ValidationResultSeverity.cs`, `Validation/ValidationResultReason.cs` |
| Every nonzero `status` | `Http/EndpointRouteHelper.cs`, not an HTTP host invocation |
| Generic exception message | `ExceptionDetailRedactor.cs`; these fixtures supply redacted text, not a redaction implementation |
| Malformed command message | `Commands/CommandResult.cs`, `InvalidBody` |

Secondary cross-port expectations:

- [Arc.TypeScript at cbb421aa1fb6e5f65deec746911ee3046dfb1af6](https://github.com/Cratis/Arc.TypeScript/tree/cbb421aa1fb6e5f65deec746911ee3046dfb1af6):
  `ContractTests/Http/conformance.test.mjs`, `command`, `query`, `rule`,
  `malformedDotNet` and `dotnetReaderFailure`; published C# fixture **22.45.0**.
  Its full-envelope assertions corroborate required empty fields and numeric severities.
- [Arc.Kotlin at 23c93a3f70008f20d99c28868010b08e3123ea97](https://github.com/Cratis/Arc.Kotlin/tree/23c93a3f70008f20d99c28868010b08e3123ea97):
  `ContractTests/HttpConformance/contract.py`, `success` and malformed-input assertions;
  published C# fixture **22.14.0**. These are selected assertions, not complete envelopes.

These three profiles remain distinct. This suite targets the pinned current C#
source; it does not certify either historical host or its entire conformance suite.
Illustrative IDs/names replace generated values. The asymmetric UUID deliberately
makes accidental .NET Guid mixed-endian byte conversion visible.

## Scalars

`scalars.json` uses .NET Guid `D`, DateOnly `O`, TimeOnly `O` and TimeSpan `c`
representations. Arc installs Fundamentals' date/time converters through
`JsonSerializerOptionsConfiguration.cs`; TimeSpan uses System.Text.Json's built-in
converter. The consulted Fundamentals revision is
`d2accc4a79b6bcf2708213c97093ab5ba6c06381`, with sources:

- `Source/DotNET/Fundamentals/Json/DateOnlyJsonConverter.cs`
- `Source/DotNET/Fundamentals/Json/TimeOnlyJsonConverter.cs`
- `Source/DotNET/Fundamentals/Serialization/AcronymFriendlyJsonCamelCaseNamingPolicy.cs`
- `Source/DotNET/Fundamentals/Strings/StringExtensions.cs`

The scalar fixture test compares literal JSON bytes. The envelope test compares
canonical JSON bytes after **only** whitespace and object-key-order normalization;
array order, numbers, missing properties, null and scalar zeros are not normalized.
C# generic inheritance and resolver choices may change property ordering, which
is not a JSON contract. This is not a promise of identical arbitrary JSON escaping.

## Boundaries

All 21 envelope fixtures and 11 scalar fixtures must execute. Status zero labels a
standalone validation value, not an HTTP response. Failed scalar response omission
has a separate regression test and is a documented intentional C# correction.

The foundation tests exercise result/scalar implementations. Hosting evidence is
separate: `ContractTests/hosting_test.go` executes all six `hosting.json` cases and
all five `discovery.json` cases through an actual built and started application.
The same test runs the nine task-board cases using real registrations and pipelines.
`lifecycle_test.go` adds real loopback listener, HEAD and graceful-shutdown evidence.
These tests are Go-owned adaptations, not paired .NET or browser conformance.
Wait-timeout 408 and observable transports remain unsupported.

## Hosting sources and differences

At the same pinned Arc revision, hosting expectations come from
`Commands/CommandEndpointMapper.cs`, `Queries/QueryEndpointMapper.cs`,
`Queries/BodyQueryRequestReader.cs`, `Identity/IdentityEndpointMapper.cs`,
`Identity/IdentityProvider.cs`, `Introspection/IntrospectionEndpointMapper.cs`,
`Introspection/DiscoveryExposure.cs` and `Http/EndpointRouteHelper.cs`.
`hosting.json` deliberately records Go-specific empty 404/405, explicit HEAD in
Allow, terminal credential rejection 401 and reader-error 400 overrides.
Discovery cache headers extend `no-store, private` protection to catalogs/schema;
current C# does not uniformly apply those headers. Schemas are bounded Go metadata,
not complete .NET JsonSchemaExporter output. No automatic golden update is allowed.
