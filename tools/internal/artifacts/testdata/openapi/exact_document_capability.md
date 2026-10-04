# Exact OpenAPI document capability: Stage 1 blocked

This fixture investigates the approved exact-document/split-validation route for
[Arc.Go#24](https://github.com/Cratis/Arc.Go/issues/24). It is not a renderer,
application contract, C# parity capture, dependency admission or CLI implementation.
The full capability gate **does not pass**. Do not start the pure projection worker
on the strength of the passing numeric subset.

## Pins and source findings

The alternate manifest belongs to the existing tools module. Its Go minimum
remains `1.26.0`, and its runtime pin remains
`github.com/cratis/arc.go v0.0.0-20261003142617-78ebbf8fdff8`.
Neither library is added to any tracked manifest.

| Library | Version | Upstream commit | Declared Go minimum |
| --- | --- | --- | --- |
| kin-openapi | v0.149.0 | `1a812b4b73ede7fa295c63a5c89d1ca7250dcc07` | 1.25 |
| jsonschema/v6 | v6.0.3 | `b0fc661f4939578bc429f408c18202573dbafcc4` | 1.21 |

The installed module-cache sources and cached download origin metadata at these
pins were read. kin already requires jsonschema/v6 v6.0.3. In jsonschema,
`loader.go:UnmarshalJSON` uses `UseNumber`; `objcompiler.go:numVal` and
`validator.go:numValidate` compare `big.Rat` values. `Compiler.DefaultDraft`,
`AddResource`, `UseLoader` and `Compile` are used directly. No kin `VisitJSON`,
compiler-error fallback, rounded post-filter, patched cache or replacement module
participates in the exact checks.

## Passing subset

The test-only `OpenAPIDocument` owns exact JSON, not `openapi3.T`. Bounds are
`json.Number` values constructed directly from validated decimal strings.
Authoritative JSON declares OpenAPI `3.1.1` and `jsonSchemaDialect`
`https://json-schema.org/draft/2020-12/schema`.

The exact engine decodes those bytes and registers the **whole document** at
`https://capability.invalid/openapi.json`. All 16 actual Schema Objects in this
fixed fixture are compiled at their original JSON Pointers, including
unreferenced components, nested schemas, an inline QUERY schema and a native
operation's local-reference schema. Local `#/components/schemas/` references
remain unchanged; recursive and nullable components validate.

On Go 1.26.8, mathematical bound checks and instance validation accept signed
minimum/maximum and unsigned zero/maximum; reject the three adjacent out-of-range
numbers, negative unsigned values and fractional integers; and pass small-width,
exclusive-bound, exact `multipleOf`, numeric `const` and numeric `enum` controls.
Valid defaults/examples are checked with the exact engine. Copy ownership and
exact decode/encode determinism pass.

A fresh kin structural view loads a copy of the same authoritative bytes, with
external references disabled and a rejecting URI callback. Schema example and
default instance validation are disabled there. That view is never serialized or
published, and its numeric values are never trusted. Both views leave the original
bytes unchanged; these retained local fixtures invoke no URI callback.

## Blocking structural acceptance

`TestExactDocumentCapabilityQueryRejectsInvalidResponseKey` inserts this actual
operation under `x-cratis-query` with `method: "QUERY"`:

```json
{
  "operationId": "query",
  "responses": {
    "wrong": { "description": "invalid response key" }
  }
}
```

Document validation ignores the extension's operation contents. Independently
unmarshalling the operation from the same authoritative JSON and calling its
actual `openapi3.Operation.Validate` also returns nil. `"wrong"` is neither a
response status code/range nor `default` nor an `x-` extension.

At the pin above, `operation.go:Operation.Validate` delegates responses to
`response.go:Responses.Validate`, which validates response values but does not
validate their keys. The executable assertion requires rejection and therefore
**exits 1**. It must not be inverted or weakened. No fake POST or native 3.2
operation is substituted. Work stopped at this false acceptance; no new general
structural validator or response-key bypass was added.

An initial broader diagnostic also confirmed `loader.go:loadSingleElementFromURI`
lets a custom `ReadFromURIFunc` own access policy: installing it means
`IsExternalRefsAllowed=false` alone does not prevent invocation. A callback that
always returns an error performs no I/O. The initial diagnostic log is retained
outside Git; this is not evidence that an external fetch occurred.

## Reproduce

Create only an ignored alternate manifest for the existing tools module:

```bash
mkdir -p .ai-work/keep/openapi-capability-stage1
cp tools/go.mod .ai-work/keep/openapi-capability-stage1/capability.mod
cp tools/go.sum .ai-work/keep/openapi-capability-stage1/capability.sum
GOWORK=off GOTOOLCHAIN=local go mod edit \
  -modfile=.ai-work/keep/openapi-capability-stage1/capability.mod \
  -require=github.com/getkin/kin-openapi@v0.149.0 \
  -require=github.com/santhosh-tekuri/jsonschema/v6@v6.0.3
cd tools
```

Use an actual Go 1.26 binary and put its directory first on `PATH`. The executed
binary was
`/Users/sindrewilting/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.8.darwin-arm64/bin/go`.
Run each command separately through the bounded phase runner where required:

```bash
# Original immutable cd9d487 fixture: EXPECT FAIL, six numeric failures.
GOWORK=off GOTOOLCHAIN=local go test \
  -modfile=../.ai-work/keep/openapi-capability-stage1/capability.mod -mod=mod \
  -count=1 -timeout=90s -v \
  ./internal/artifacts/testdata/openapi/kin_openapi_capability_test.go

# Compiled exact-number and ownership subset: PASS, exit 0.
GOWORK=off GOTOOLCHAIN=local go test \
  -modfile=../.ai-work/keep/openapi-capability-stage1/capability.mod -mod=readonly \
  -count=1 -timeout=90s -v \
  -run '^TestExactDocumentCapability(Numbers|Ownership)$' \
  ./internal/artifacts/testdata/openapi/exact_document_capability_test.go

# Structural capability blocker: EXPECT FAIL, exit 1; Stage 1 is not green.
GOWORK=off GOTOOLCHAIN=local go test \
  -modfile=../.ai-work/keep/openapi-capability-stage1/capability.mod -mod=readonly \
  -count=1 -timeout=90s -v \
  -run '^TestExactDocumentCapabilityQueryRejectsInvalidResponseKey$' \
  ./internal/artifacts/testdata/openapi/exact_document_capability_test.go
```

The original six assertions and fixture bytes remain unchanged. Both fixtures
live under `testdata` and do not join ordinary tools `./...` tests. No broad
runtime, integration or existing-tools gate was run for this failed proof.

## Not admitted

The retained fixture is deliberately minimal after the stop condition. It does
not establish a general schema inventory, QUERY reference binding/security/ID
validation, malformed/unsupported-keyword admission, a complete no-I/O refusal
matrix, bad default/example rejection, format assertion policy or representable
count/length admission. In particular, jsonschema `objcompiler.go:intVal` converts
through `Int64()` to `int`: a future approved route must reject unrepresentable
count/length keywords before compilation, without restricting numeric bounds.

Draft 2020-12 formats default to annotations unless `AssertFormat` is selected.
This fixture makes no .NET time-precision or uint64-format acceptance claim.
Dialect changes, `$id` rebasing, dynamic references, custom vocabularies and
content assertions remain outside the admitted capability. Root/tooling
production code, `Generate`, graph, writers and CLI remain unchanged.
