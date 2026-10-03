# Shared wire-contract expectations

These expectations are independently authored **source-derived fixtures**, not
captured C# responses or generated OpenAPI documents. `presence.json` exercises
Arc.Go serialization at the fetchable tools runtime pin. The graph assertions in
`wire_contract_test.go`, `contract_edges_test.go` and `wire_regressions_test.go`
are separate expectations; the tests never generate expected wire instances
from the graph.

`corrections.go` is both the graph input and the independently compiled consumer
for `corrections_test.go`, using the tools module's fetchable runtime pin without
a workspace or replace. It witnesses invariant TimeSpan strings across the full
signed tick range, calendar strings, pointer/Optional absence-null-zero-value
behavior, and named signed/unsigned validation state. The graph assertions also
round-trip the compiler-free framework projection and retain nullable value
contracts separately from the validation envelope's encoded-null omission.

C# reference revision: `7c1e78075b737df64f69fddfaae83374f75e3612`:

- `Source/DotNET/Arc.Core/JsonSerializerOptionsConfiguration.cs`:
  acronym-friendly naming, null omission and named floating-point literals.
- `Source/DotNET/Arc.Core/ArcDefaultsJsonTypeInfoResolver.cs`:
  application metadata can override the framework defaults. Static tooling
  cannot infer an arbitrary application's custom resolver behavior.

The Go source at observable candidate `0832cb227e7c6f8b1c0a3744206995c7028af594`
is authoritative for the tested Go-specific details:

- `serialization/{marshal,unmarshal,optional,fields}.go`: fresh-zero binding,
  scalar null rejection, fixed-array length checking, property nil omission,
  null collection elements, `omitempty`, `omitzero` and Optional presence.
- `internal/modelshape/{fields,select}.go`: shared compiler/runtime selection,
  embedded-member dominance and explicit JSON tag behavior.
- `queries/binding.go`: required/default flags, CSV and repeated-value binding,
  case-insensitive argument names, and empty/null-as-missing handling.
- `commands/result.go`, `queries/result.go`, `queries/paging.go`,
  `validation/result.go`, `identity/view.go`: required arrays and strings,
  output payload omission, response numeric widths and identity null details.
- `authorization/{registry,declarations}.go`, `options.go`, `routes.go`:
  static declaration validation, server defaults and discovery exposure.

The Go pointer/Optional and `omitzero` cases are not claims that C# uses those Go
representations. Enum members and flags describe values; the Go runtime's accepted
integer domain remains open. A hosted C# capture, document renderer and ordinary
OpenAPI consumer witness are separate evidence, not supplied by these fixtures.
