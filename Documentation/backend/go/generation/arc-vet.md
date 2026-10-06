---
title: Arc authoring diagnostics
description: Check model-bound Go declarations with arc-vet and interpret their ARC diagnostic codes.
---

A declaration can compile without being discoverable as an Arc command or query.
The experimental `arc-vet` command reports these authoring mistakes without
executing application code or writing adapters. It also reports unused preparation
values, potentially absent read-model dependencies and contradictory authorization.
Use [generator directives and signatures](reference.md) for the admission contract.

## Run the checker

Build from the `tools` module, then run the executable in your application module:

```bash
# In Arc.Go/tools:
GOWORK=off GOTOOLCHAIN=local go build -o arc-vet ./cmd/arc-vet

# In your application module, use the executable's absolute path:
/path/to/arc-vet ./...
GOFLAGS=-tags=myprofile /path/to/arc-vet ./...
/path/to/arc-vet -json ./...
```

Your application must type-check, including existing generated adapters.
Build tags, GOOS and GOARCH select the inspected declarations; check each supported
configuration separately. Generated files are not inspected. The checker does
not generate code or replace `arc-gen -check`, registration checks or runtime
validation. No automatic fixes are supplied.

## Diagnostic reference

These rules translate the pinned C# analyzers under
`Source/DotNET/Arc.Core.CodeAnalysis` at Arc `7c1e78075b737df64f69fddfaae83374f75e3612`.
The Go rules use directive opt-in and compiler type identity, including aliases;
matching a framework type's spelling or structure is not enough.

| Code | C# analyzer | Go meaning and recovery |
| --- | --- | --- |
| ARC0001 | `ReadModelAnalyzer.cs` | An explicit query, or implicit method returning its owning-model shape or a recognized observable source, has an invalid result signature or owning-model shape. Return `(O, error)` with an admitted model/source shape. Unrelated implicit helper returns remain outside Go discovery. |
| ARC0002 | `CommandAnalyzer.cs` | In a package with artifact-selection directives, a struct with exported nonembedded data and a declared `Handle` lacks an artifact directive or a typed `commands.Register[T]` reference in the same package. Choose `arc:command` for generated registration or `commands.Register[T](builder, commands.Handle(T.Handle))` for manual registration; do not register through both paths. Use `arc:ignore` on a non-command or a command registered from another package or through a generic wrapper. Packages without artifact-selection directives, other selected artifacts, empty service receivers, `Helper`/`Helpers`/`Extensions` suffixes and implementations of `commands.ResponseValueHandler` do not trigger the heuristic. |
| ARC0003 | `CommandAnalyzer.cs` | A non-command receiver declares `Handle` taking an opted-in command. Move handling onto the command. Package-level functions are not handler methods. |
| ARC0004 | `CommandAnalyzer.cs` | An opted-in command lacks a directly declared exported `Handle`. Lowercase, promoted and ignored methods do not count. |
| ARC0005 | `CommandProvideAnalyzer.cs` | `Provide` returns a payload that no `Handle` parameter consumes by exact Go type identity, including the payload of `commands.Preparation[P]`. Return a control-only result or add the payload parameter. Unlike C#, assignability alone is insufficient; multiple exact matches remain an arc-gen error. |
| ARC0006 | `InjectedReadModelAnalyzer.cs` | Command `Handle`/`Provide` or an `arc:validator` constructor takes an opted-in read model by value. Consider explicit absence handling, or deliberately retain required-dependency failure. A pointer avoids this warning but does not make an ordinary DI dependency optional: your resolver must implement the intended missing-model behavior. |
| ARC0014 | `ReadModelAnalyzer.cs` | An explicit generic function or implicitly discovered generic-receiver method returns its owning query shape. Use a concrete declaration or move/ignore a composition helper. From Go 1.27, this also reports discovered query methods with independent method type parameters. |
| ARC0015 | `QueryParameterConceptTypeAnalyzer.cs` | An admitted query directly converts an exported primitive argument field to a Fundamentals concept. Declare the field as the concept if its validation and presence semantics fit the input. This is a bounded syntax check, not dataflow analysis; see the [ARC0015 command reference](https://github.com/Cratis/Arc.Go/blob/develop/tools/cmd/arc-vet/README.md#arc0015-primitive-argument-converted-to-a-concept). |
| ARC0019 | `AuthorizationAttributeAnalyzer.cs` | The same opted-in model/query declaration has both `arc:allow-anonymous` and `arc:authorize` (bare, roles or policy). Keep one level. A query's anonymous override of model-level authorization is valid. |

The declaration analyzer reports before arc-gen admission, so malformed command
or query shapes and authorization conflicts can have both an ARC finding and an
admission error from the ARC0015 analyzer. ARC0015 still requires the package's
handwritten declarations to pass shared generator admission before inspecting
query bodies. Unrelated malformed directives remain generator errors.

`arc:ignore` excludes a declaration from these new convention checks; it is not
a runtime authorization bypass. ARC0002 recognizes typed `commands.Register[T]`
references only in the type's own package, including aliases and inferred type
arguments. It cannot see registration from an importing package or trace a
user-defined generic wrapper whose `commands.Register[T]` type argument is a type
parameter. Put `//arc:ignore` on the command type in either case to suppress
ARC0002; keep the manual registration unchanged. The declaration rules also do
not inspect arbitrary manual registration callbacks, runtime blank-field metadata
tags or Chronicle projection definitions. Imported opted-in command/read-model identities are
transported by Go analysis facts for the selected build, so imported aliases do
not lose their meaning. Constructors are checked only when explicitly marked
`arc:validator`, not merely because their names resemble validators.

## C# rules that do not apply to Go

These are deliberate language/host differences, not silently implemented rules.

| Code | C# analyzer | Why it is not applicable |
| --- | --- | --- |
| ARC0007 | `ModelBoundRecordAnalyzer.cs` | Go commands are structs; there is no class-versus-record declaration choice. |
| ARC0008 | `ModelBoundRecordAnalyzer.cs` | Go read models are structs; there is no CLR record equality/immutability declaration. |
| ARC0009 | `ConceptRecordAnalyzer.cs` | Go concepts use named types and codecs, not inheritance from `ConceptAs<T>` or a class-versus-record choice. |
| ARC0010 | `CommandHandleTaskWrappingAnalyzer.cs` | Go handlers return synchronous values/errors; there is no `Task`, `async` or `await` wrapper to remove. |
| ARC0011 | `RolesLiteralAnalyzer.cs` | Go has neither `nameof` nor attribute arguments. Role directives contain explicit strings; there is no compiler-linked enum-member spelling to suggest. |
| ARC0012 | `ArcArtifactBuiltInExceptionAnalyzer.cs` | Go ordinary failures use returned errors, not a CLR built-in exception hierarchy. This does not endorse panics or uninformative errors. |
| ARC0013 | `ValidatorConceptDereferenceAnalyzer.cs` | Go validation does not use FluentValidation expression-tree selectors over nullable `ConceptAs<T>` references. Pointer safety remains the validator author's responsibility. |
| ARC0020 | `AuthorizationAttributeAnalyzer.cs` | There are no ASP.NET attributes or an ASP.NET integration assembly whose absence leaves attributes unenforced. |
| ARC0021 | `AuthorizationAttributeAnalyzer.cs` | There is no ASP.NET authentication-scheme attribute setting. Unsupported generator directive options already fail admission. |

## Output and failure handling

Text output includes `file:line:column`, an ARC code and a recovery message.
Exit `0` means no findings, `3` means findings, and `1` means package loading or
analysis failed. A failed analysis can coexist with declaration findings; it is
not a clean result. Invalid flags can also fail.

The standard `-json` report uses analyzer keys `arcdeclarations` and `arcauthoring`
and ARC codes in the `category` field. JSON mode can return zero despite findings
or analysis errors: inspect both diagnostic and error records. Use default text
mode for an exit-status gate, and use the built executable rather than `go run`
when distinguishing exit codes.
