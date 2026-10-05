# arc-vet

`arc-vet` reports authoring mistakes in Arc command and query declarations. It does
not generate adapters, execute application code, or replace runtime validation.

Build the command from the `tools` module, then run it in the application module:

```sh
# In Arc.Go/tools:
GOWORK=off GOTOOLCHAIN=local go build -o arc-vet ./cmd/arc-vet

# In the application module, using the built executable's absolute path:
/path/to/arc-vet ./...
GOFLAGS=-tags=myprofile /path/to/arc-vet ./...
```

The application must type-check, including any existing generated adapters. The
ARC0015 check shares arc-gen's declaration and query-signature admission.
The separate declaration analyzer reports ARC0001-0006, ARC0014 and ARC0019
before that admission, including command-like structs missing opt-in. Invalid
declarations and concept definitions can fail ARC0015 analysis alongside
those findings. Generated files are not inspected.

See the [authoring diagnostic reference](https://github.com/Cratis/Arc.Go/blob/develop/Documentation/backend/go/generation/arc-vet.md)
for each rule's Go meaning, pinned C# source, limitations and rules that do not
apply to Go.

## ARC0015: primitive argument converted to a concept

Go queries receive a named argument struct rather than individual HTTP
parameters. This rule reports a direct conversion of an exported, top-level
primitive argument field into a concept inside an admitted query body:

This excerpt assumes an admitted `Customer` query and an existing
`findCustomer` implementation:

```go
func (Customer) ByName(args FindCustomer) (Customer, error) {
    name := CustomerName(args.Name)
    return findCustomer(name)
}
```

If `FindCustomer.Name` is a `string` and `CustomerName` is a concept recognized by
Fundamentals, the diagnostic points to `Name` in `args.Name`. Declare the argument
field as the concept and review its validation, presence and binding behavior;
do not mechanically change an intentionally less constrained search input.

The rule recognizes built-in string, boolean, integer and floating-point fields,
including aliases, and concept aliases/imports through the shared type
classifier. It only checks queries admitted by `arc:readmodel` and `arc:query`
conventions. Similar names or method signatures on non-Arc types do not opt in.

This is deliberately a syntax check, not dataflow analysis. It does not follow
local copies, factories, closures, service parameters, pointers, UUIDs, distinct
named primitive types, nested/promoted fields, unexported fields or `json:"-"`
fields. An absence of diagnostics is not proof that validation cannot be
bypassed. It supplies no automatic fix or per-site suppression. Runtime
validation remains authoritative.

This is a partial Go translation of
[Arc's ARC0015 analyzer at 7c1e780](https://github.com/Cratis/Arc/blob/7c1e78075b737df64f69fddfaae83374f75e3612/Source/DotNET/Arc.Core.CodeAnalysis/QueryParameterConceptTypeAnalyzer.cs),
not the complete C# analyzer suite. Operation and decision diagnostics are not
implemented.

## Output and exit status

The default text output includes `file:line:column`, the ARC code, and a recovery
message. ARC0015 also names the field and concept types. It reports each matching conversion separately. With the standard
Go analysis driver, exit `0` means no findings, `3` means findings, and `1` means
loading or analysis failed. Invalid command-line flags can also fail.

`-json` emits the standard Go analysis report, including ARC diagnostic categories
under analyzer keys `arcdeclarations` and `arcauthoring`. **JSON mode can return zero despite findings or analysis errors:**
consumers must inspect both diagnostic and error records. Use default text mode
for an exit-status gate. `go run` can wrap the executable's nonzero exit status;
use the built command when distinguishing exit codes.

Build constraints select the inspected source just as they do for `go build`.
Checking one build configuration does not check excluded files or other targets.
