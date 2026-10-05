# Screenplay metadata export contract fixture

This is a pure **partial metadata** export, not Graph JSON, a complete
application model or a code-generation input. `arc-gen -screenplay-out` publishes
it as a manifest-owned file prefixed with the generated-code header line.
Every returned document carries the same omission diagnostics as its result.
Unknown or ambiguous shapes fail without document bytes. No source analysis runs
inside the exporter; `Graph` is the only input.

## Authorities

- Arc commit `7c1e78075b737df64f69fddfaae83374f75e3612`:
  `Source/DotNET/Screenplay/IScreenplayGenerator.cs`,
  `Emission/ApplicationSyntaxBuilder.cs`, `Emission/Queries/QuerySyntaxBuilder.cs`,
  `Emission/Types/ScreenplayPrimitiveTypes.cs` and `Directory.Packages.props`.
  Arc pins `Cratis.Screenplay` **4.48.1**. Its primitive mapper explicitly warns
  against inferring Int/Decimal or Date/DateTime from TypeScript hints.
- Screenplay **4.48.1**, commit
  `e5b5698f9dbcf61c39e4cc3f43fcf092c9e0b5fc`:
  `Source/DotNET/Screenplay/Printing/ScreenplayPrinter.cs`,
  `Printing/ScreenplayPrinter.Commands.cs`, `Printing/ScreenplaySyntaxText.cs`,
  `Parsing/PropertyLineParser.cs`, `Parsing/QueryParser.cs`,
  `Parsing/SliceParser.cs`, `Parsing/ScreenplayValidator.cs` and
  `Text/ReservedWords.cs`.
  The cached package's NuGet repository metadata names this same commit.

`metadata.play` is compared byte-for-byte with Go exporter output. The dedicated
`compiler` fixture consumes the real, exactly pinned NuGet compiler, not a local
replacement grammar. Its lock file includes all three package content hashes.
It requires zero compiler diagnostics, checks parsed command/read-model/query
shapes, and compiles the official printer's result. Negative controls reject Graph
JSON and require an undeclared-type diagnostic. The upstream compiler reports
unknown types as warnings; this fixture does not misrepresent that as an upstream
error. Compilation here proves document syntax and references, not executable
admission, event-model completeness or runtime behavior.

## Admitted descriptive subset

- Existing normalized command inputs and snapshot/observable query results.
- Plain model types; String, Bool, Uuid, Int, Decimal and DateTime references.
  Numbers require `WireContract.Scalar.GoKind`; DateTime requires the original
  `time.Time` declaration. Ambiguous legacy hints are rejected.
- One collection suffix and one optional suffix. Nested collections, nullable
  elements, maps, enums, opaque schemas/codecs, concepts and derived models are
  refused rather than flattened or renamed.
- ASCII declaration names and lowercase/underscore-leading wire property names.
  Property escapes preserve directive-shaped names such as `authorize`.
- Globally distinct simple model and command names. Cross-namespace collisions,
  repeated packages/declarations/fields, catalog omissions/conflicts and excluded
  descriptors fail. Types are also emitted as read models where queries need
  them; these are separate Screenplay declaration categories.

The fixed `Application/Metadata` grouping and per-artifact slices are presentation,
not inferred domain architecture. Query arguments are filters; required input is
not evidence of a Screenplay `by` key. Paging/sorting, affected authorization,
response and field-rule/default omissions receive artifact-specific diagnostics.
Universal limitations remain visible even when Graph contains no such metadata:
absence from this graph is not evidence that an application lacks a capability.

## Deferred shared descriptors

No parallel source-analysis graph was introduced. Broader export needs additions
to the **shared** graph and its owning analyzer:

- A capability inventory distinguishing analyzed-empty from unsupported/unknown
  and incomplete source coverage, including manual registrations.
- Event identity, generation, source ownership, classification and field shape;
  command produced-event edges, conditions, mappings and event-source targets.
- Read-model identity and projection/reducer provenance, source event generations,
  keys, mappings, children, removals and implementation references.
- Domain/module/feature/slice identity and placement, instead of treating Go
  package names or HTTP routes as an event model.
- Original concept/enum declarations, enum parse names and domains, richer type
  references, validation and authorization/policy semantics, query key intent,
  decision reads, concurrency, reactions, constraints, screens and specifications.
- Repository-relative, unambiguous implementation locations: the existing source
  basename cannot safely become a Screenplay `file` reference.

CLI/check publication is implemented by `arc-gen`. An embedded viewer is
deliberately not implemented. No
route is added and no Go environment variable is interpreted as C# Debug mode.

## Run the bounded witnesses

From `tools/`, using the module's Go 1.26 minimum without a workspace:

```sh
GOWORK=off GOTOOLCHAIN=local go test -count=1 -timeout=90s ./internal/artifacts -run '^TestScreenplay'
```

For the real compiler witness, use .NET SDK 10.0.401 and the cached pinned packages.
From the repository root, run restore, build and execution as separate phases:

```sh
dotnet restore tools/internal/artifacts/testdata/screenplay/compiler/Compiler.csproj \
  --locked-mode --source "$HOME/.nuget/packages" -p:NuGetAudit=false \
  -p:BaseIntermediateOutputPath="$PWD/.ai-work/screenplay/obj/"

dotnet build tools/internal/artifacts/testdata/screenplay/compiler/Compiler.csproj \
  -c Release --no-restore -p:BaseIntermediateOutputPath="$PWD/.ai-work/screenplay/obj/" \
  -o "$PWD/.ai-work/screenplay/bin/"

dotnet .ai-work/screenplay/bin/Compiler.dll \
  tools/internal/artifacts/testdata/screenplay/metadata.play
```

Missing cached packages make this offline witness unavailable, not passed. It is
an explicit check: ordinary Go `./...` does not execute this .NET fixture. Disabling
NuGet audit for the offline restore is not a vulnerability assessment. The
repository's full native/CI gates are separate from these focused witnesses.
