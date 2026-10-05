# OpenAPI dependency capability gate

This fixture tests prerequisites for the internal renderer in
[Arc.Go#24](https://github.com/Cratis/Arc.Go/issues/24), not an emitted application
contract or a C# capture. It deliberately lives under `testdata`: the selected
dependency is not admitted to the tools module until the capability gate passes.

## Pinned blocker

`github.com/getkin/kin-openapi v0.149.0` (upstream commit
`1a812b4b73ede7fa295c63a5c89d1ca7250dcc07`) declares Go 1.25 and compiles on
Go 1.26.8 without raising the tools minimum. Representative OpenAPI 3.1.1 null
unions, document-local references, instance checks and deterministic serialization
pass. Exact numeric semantics do not:

| Schema bound | Required value | Serialized value |
| --- | --- | --- |
| Signed minimum | -9223372036854775808 | -9223372036854776000 |
| Signed maximum | 9223372036854775807 | 9223372036854776000 |
| Unsigned maximum | 18446744073709551615 | 18446744073709552000 |

`VisitJSON` with `EnableJSONSchema2020()` also accepts the out-of-range
`json.Number` instances `-9223372036854775809`, `9223372036854775808` and
`18446744073709551616`. The positive extrema and zero, negative-unsigned and
fractional controls prevent a vacuous failure report.

The pinned source exposes `Schema.Min`, `Schema.Max` and
`ExclusiveBound.Value` as `*float64` in `openapi3/schema.go`.
`newJSONSchemaValidator` in `openapi3/schema_jsonschema_validator.go` marshals
the schema and decodes it into `map[string]any` without `UseNumber`. The 2020-12
entry point also falls back to the built-in validator on compilation failure.
The fixture asserts mathematical equality with `big.Rat`, not merely a decimal
spelling convention. Its failing checks must not be inverted or weakened.

## Reproduce without retaining the dependency

From the repository root, create an ignored alternate manifest for the existing
tools module; this is not a new module or a local runtime replacement:

```bash
mkdir -p .ai-work/keep/openapi-renderer
cp tools/go.mod .ai-work/keep/openapi-renderer/capability.mod
cp tools/go.sum .ai-work/keep/openapi-renderer/capability.sum
cd tools
go mod edit -modfile=../.ai-work/keep/openapi-renderer/capability.mod \
  -require=github.com/getkin/kin-openapi@v0.149.0
GOWORK=off GOTOOLCHAIN=local go test \
  -modfile=../.ai-work/keep/openapi-renderer/capability.mod -mod=mod \
  -count=1 -timeout=90s -v \
  ./internal/artifacts/testdata/openapi/kin_openapi_capability_test.go
```

Use the installed Go 1.26 toolchain to check the supported minimum. Run the test
through the session's bounded phase runner when required. At the pin above, the
command exits 1: three schema-bound checks and three out-of-range instance checks
fail. This is a blocked capability gate, not a passing renderer test.

The renderer, operation/security/extensions validation, graph-to-document
witnesses, external-reference refusal matrix and broader unrepresentable-value
checks remain unimplemented or unverified in this historical gate. `Generate`
now publishes the renderer's document as a file through `-openapi-out` (see
`Documentation/backend/go/generation/openapi.md`); that publication does not
change this gate's findings. No schema weakening, float rounding, version substitution or alternate
validation engine is authorized by this fixture.

## Query arguments consumer

`openapi_arguments_consumer_test.go` (package `artifacts`, build-ignored) loads
the arguments/paging fixture document with kin's standard OpenAPI 3.1 loader and
legacy router, validates GET/HEAD query parameters with `openapi3filter`, and
asserts that QUERY stays unrouted. Run it from `tools/` with the alternate
manifest above and an overlay replacing that file with a copy without its
`//go:build ignore` line:

```bash
GOWORK=off GOTOOLCHAIN=local GOPROXY=off go test -mod=mod \
  -modfile="$CAPABILITY_MOD" -overlay="$OVERLAY_JSON" -count=1 -timeout=90s \
  -run '^TestOpenAPIQueryArgumentsStandardConsumer$' ./internal/artifacts/
```
