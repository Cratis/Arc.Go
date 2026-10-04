<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

# Official-structure Stage 1 capability: URI and map traversal corrections

This test-only experiment for issue #24 is not a production renderer, an
exported validator, or a general OpenAPI conformance claim. The normative
[OpenAPI 3.1.1 specification](https://spec.openapis.org/oas/v3.1.1.html)
wins over the informative structural schema and the engine.

Both native and actual QUERY operations reject `responses: {"wrong": ...}`
and `externalDocs.url: "not a uri with spaces"`. The original 326 leaf subjects
now pass, including both unchanged URI rejection assertions. Another 218
checks exercise pinned upstream URI cases. All 544 subjects remain unchanged;
332 new controls cover `x-`-named map entries and genuine extension data.
Renderer work still requires review of this capability and its bounded scope;
passing this proof is not #24 acceptance.

## Pinned upstream resources

Upstream: [OAI/spec.openapis.org](https://github.com/OAI/spec.openapis.org),
commit `619e0bac8e4547e0f61c70cefa1043030b85cc33`.

- `official-3.1-schema.json` contains untouched raw bytes from
  [the 2026-08-03 OpenAPI 3.1 schema](https://raw.githubusercontent.com/OAI/spec.openapis.org/619e0bac8e4547e0f61c70cefa1043030b85cc33/oas/3.1/schema/2026-08-03).
  Size: 33,483 bytes. SHA-256:
  `59f106413cb48c31299f96f024c938d3628aed6cd02cd14bcfb2fcaae7a130b6`.
- `official-3.1-LICENSE` contains the full, unchanged Apache-2.0
  [upstream license](https://raw.githubusercontent.com/OAI/spec.openapis.org/619e0bac8e4547e0f61c70cefa1043030b85cc33/LICENSE)
  from the same commit. Size: 11,357 bytes. SHA-256:
  `c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4`.

Both resources are embedded and their raw hashes and lengths asserted. The
schema is registered offline under its original `$id`,
`https://spec.openapis.org/oas/3.1/schema/2026-08-03`. This is **schema**, not
**schema-base**: the latter forces the OpenAPI dialect, while the authoritative
application document declares `https://json-schema.org/draft/2020-12/schema`.
Trusted official `$dynamicRef` infrastructure is unchanged; application dynamic
references and anchors remain outside the admitted capability.

The URI controls come unchanged from
[JSON-Schema-Test-Suite commit ab079cc2bace029fdbb483be28a6ade526bcfbc2](https://github.com/json-schema-org/JSON-Schema-Test-Suite/tree/ab079cc2bace029fdbb483be28a6ade526bcfbc2).
They are format test inputs, not Cratis wire fixtures or a C# capture:

| Local resource | Upstream path | Bytes | SHA-256 |
| --- | --- | ---: | --- |
| `uri-tests.json` | `tests/draft2020-12/optional/format/uri.json` | 8,893 | `47954ee6aef87c20a045ad2182fecd9c7eec53b6c8151ee88cba50381b2850a4` |
| `uri-reference-tests.json` | `tests/draft2020-12/optional/format/uri-reference.json` | 7,771 | `3a9913d43edd31650d3c4f8cc75b2b701bd6670c514cc759f4586245f72c59cb` |
| `uri-tests-LICENSE` | `LICENSE` (MIT) | 1,057 | `837402bd25fad9b704265801ca3f92566a98157c1f9a7acd6f446299ba1c305a` |

All five raw resources have embedded hash and length assertions. The complete
URI groups also assert their schema/dialect and exact counts (47 and 31).

## Validation route

`official_structure_capability_test.go` uses the preserved copy-owned exact
JSON DTO and `jsonschema/v6` **v6.0.3**, pinned only in an ignored alternate
manifest for the existing tools module. No tracked dependency is changed.
Kin **v0.149.0** remains solely in the immutable historical witness, not in this
new acceptance path.

The same authoritative bytes are decoded with `UseNumber` through the engine's
`UnmarshalJSON`. Exact physical bounds are checked with `big.Rat`. Validation
never serializes a kin document, rounds a bound, or rewrites a published `$ref`.

1. The unmodified official schema validates the complete OpenAPI document.
2. A small whole-document overlay applies the original official
   `$defs/operation` to `/paths/{path}/x-cratis-query/operation` at its actual
   instance pointer. It requires the QUERY method/envelope shape. It does not
   invent POST, flatten definitions, or change the OpenAPI version.
3. Context traversal inventories actual Schema Objects, typed reference-bearing
   objects, and operations, including unused components and nested schema-valued
   keywords. Examples, defaults, and arbitrary extension data are not searched
   for schema-looking keys.
4. Bounded relationships check typed local pointers, productive Reference Object
   chains, operation ID uniqueness, named/resolved security schemes, duplicate
   resolved `(name, in)` parameters, and path-template consistency. Operation
   overrides are legal. Empty security arrays, anonymous `{}` requirements,
   inheritance, and non-OAuth role arrays remain valid.
5. Every discovered schema is compiled at its original JSON Pointer within the
   **same whole-document resource**. Exact default/examples and OpenAPI examples
   are checked against those compiled schemas.

The official Responses definition admits `default`,
`^[1-5](?:[0-9]{2}|XX)$`, and legitimate `x-` extensions; it rejects unknown keys,
malformed values, and extensions-only Responses Objects. Empty response
`description` is valid. OpenAPI 3.1 operations need not always have `responses`.

## URI format semantics

The pinned engine's built-in `uri` and `uri-reference` checks use `net/url`,
which accepts syntax outside RFC 3986, including spaces and non-ASCII paths.
The proof compiler registers replacements for those two formats through
`Compiler.RegisterFormat`, before compiling the unchanged official schema.
This is an explicit engine integration, not an assertion post-filter or a
change to the official dialect. No dependency version is changed.

`official_uri_capability_test.go` translates
[RFC 3986 Appendix A](https://www.rfc-editor.org/rfc/rfc3986#appendix-A)
productions into anchored grammar expressions, using `net/netip` for IPv6
address syntax. It preserves percent-encoded text, relative and empty
references, non-HTTP schemes, reg-name hosts, and the URI/IRI distinction.
It does not normalize, trim, resolve, fetch, or require a reachable host.
[OpenAPI 3.1.1 relative-reference rules](https://spec.openapis.org/oas/v3.1.1.html#relative-references-in-api-description-uris)
allow relative references even in URI fields historically named `url`;
requiring HTTPS or an absolute URL here would incorrectly narrow the contract.

Structural validation always opts into format assertions. Application schema
formats retain the explicit annotation/assertion choice for Draft 2020-12.
All 78 upstream cases run in both modes; all 31 URI-reference cases also run
through native and QUERY `externalDocs.url`. Non-string values remain valid
for a format-only schema but fail the official URL property's string type.

## Context-specific extension handling

An `x-` prefix identifies extension fields only on Objects that permit
Specification Extensions. It does not turn a named map entry into opaque data.
In particular, `responses.200.headers.x-test` is a Header Object or Reference
Object, not an extension. The previous generic map skip could hide its schema
or external reference from admission.

The traversal now distinguishes the contracts in the pinned official schema:

- Paths and Responses Objects retain their extension skip, as does traversal
  of a Callback Object's expression fields.
- Response and encoding `headers`, response `links`, operation `callbacks`,
  parameter/header/media `examples`, media `encoding`, and `content` maps visit
  every entry. An `x-` content key must satisfy the same finite content-key
  admission as any other key; it is not an extension escape hatch.
- Top-level `webhooks` visits every named Path Item, including `x-` names.
  Component maps and schema-valued maps already visit all entries and retain
  that behavior. Unknown schema keywords still fail the bounded admission list.

`official_extensions_capability_test.go` adds native/QUERY fixtures with
`x-`-named headers, schemas, components, callbacks, examples, links, encoding
fields and webhooks. It checks original compiled pointers and paired instances,
malformed schemas, unsupported keywords, invalid defaults, local typed targets,
missing/wrong-kind targets, and HTTPS/file/relative external-reference refusal
with zero loader calls. Genuine extension payloads and unused Example values
remain opaque. The informative schema also applies its Path Item shape to
Callback extension values; that positive control uses this shape rather than
changing the pinned schema or claiming arbitrary callback payload acceptance.

## Matrix result

The compiled Go 1.26.8 ordinary and race runs each exercised **876 leaf subjects**
across 15 top-level checks: **876 passed, 0 failed, 0 skipped**. All **544** prior
subjects remain (326 original and 218 pinned URI controls); **332** map/extension
controls are additional. Targeted vet and golangci-lint v2.14.0 also pass.
This is a bounded capability result, not a CI or release-readiness claim.

- Exact expected pointer sets match the **16** numeric-fixture and **31**
  contextual-fixture Schema Objects. These are fixed-fixture expectations, not
  general discovery completeness. The contextual fixture includes nested schema
  positions, component parameters/headers/bodies/responses, callback, webhook,
  unused Path Item, native, QUERY, and encoding header schemas. Every compiled
  location retains the original decoded pointer. The new `x-` fixtures test a
  separate naming dimension without enlarging either historical pointer set.
- Signed/unsigned extrema are accepted exactly; all three adjacent out-of-range
  integers, negative unsigned values, and fractions are rejected. Small-width,
  exclusive bounds, exact multiples, constants, enumerations, nullable schemas,
  local references, and productive recursive schemas have controls.
- All eight length/item/property/contains bounds are admitted only as exact,
  nonnegative mathematical integers fitting Go `int`, **before** the engine's
  `Int64` conversion. Zero, maximum, maximum-plus-one, negative, fraction,
  mathematical integer decimal, and wrap-sized cases run for each. This does
  not constrain numeric `minimum`/`maximum`.
- Keyword admission rejects unsupported assertions, ID rebasing, alternate
  dialects, application dynamic references/anchors, vocabularies, and content
  keywords. Paired instances exercise 25 additional assertion schemas.
- UUID annotation and assertion modes differ. Codec formats (`int32`, `int64`,
  `uint64`, `float`, `double`) remain annotations, not codec/range proof.
  `media-range` is demonstrably unenforced by the engine; a finite renderer
  content-key admission list accepts only `application/json`,
  `application/problem+json`, `text/plain`, and `text/event-stream`.
- Two independent external boundaries run: 42 OpenAPI-instance reference cases
  are refused without loader calls; four compiler/metaschema probes hit only the
  rejecting loader. No HTTP/file reader is installed. The compiler has only
  preloaded resources, engine-built-in metaschemas, and local fragments.
- Historical kin numeric and QUERY failure witnesses, including their original
  assertions, remain byte-identical to `e5dc6bc`. This repair does not rerun or
  claim to fix them; the exact engine and official structural schema replace
  their rejected validation routes, not their fixtures.
- The two original native/QUERY space-containing URI negatives now pass without
  changed assertions. All upstream syntax expectations pass, including percent
  encoding, relative references, reg-name fallback and non-ASCII rejection.
  No assertions are inverted, skipped or replaced with the validator's output.

Inventory budgets are 128 levels and 10,000 typed objects. This finite profile
is not a universal Schema Object/OpenAPI validator. No HTTP consumer, renderer,
CLI integration, precision codec, or generic media-range acceptance is proved.

## Reproduction

From `tools/`, use the actual Go 1.26.8 binary in `PATH`, `GOWORK=off`,
`GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, and the ignored tools-module
`capability.mod` retaining
jsonschema v6.0.3, kin v0.149.0, minimum Go 1.26.0, and Arc runtime `78ebbf8`.
Run each command separately through `pi-phase` with a 120-second execution and
120-second queue budget:

```sh
# Expect nonzero: original six numeric failures.
go test -mod=readonly -modfile="$CAPABILITY_MOD" -count=1 -timeout=90s -v \
  ./internal/artifacts/testdata/openapi -run '^TestExact64Bit'

# Expect nonzero: unchanged historical kin QUERY witness.
go test -mod=readonly -modfile="$CAPABILITY_MOD" -count=1 -timeout=90s -v \
  ./internal/artifacts/testdata/openapi \
  -run '^TestExactDocumentCapabilityQueryRejectsInvalidResponseKey$'

# Expect zero: preserved exact numeric/ownership subset.
go test -mod=readonly -modfile="$CAPABILITY_MOD" -count=1 -timeout=90s -v \
  ./internal/artifacts/testdata/openapi \
  -run '^TestExactDocumentCapability(Numbers|Ownership)$'

# Passing bounded official-schema proof, including URI and x-named-map controls.
go test -mod=readonly -modfile="$CAPABILITY_MOD" -count=1 -timeout=90s -v \
  ./internal/artifacts/testdata/openapi -run '^TestOfficialCapability'

# Same proof with the race detector; run as a separate phase.
go test -race -mod=readonly -modfile="$CAPABILITY_MOD" -count=1 -timeout=90s -v \
  ./internal/artifacts/testdata/openapi -run '^TestOfficialCapability'

go vet -mod=readonly -modfile="$CAPABILITY_MOD" \
  ./internal/artifacts/testdata/openapi

GOFLAGS="-mod=readonly -modfile=$CAPABILITY_MOD" golangci-lint run \
  --timeout=90s --config=../.golangci.yml ./internal/artifacts/testdata/openapi
```

The proof commands explicitly address this `testdata` package; ordinary `./...`
discovery does not select it. Broad root/tools/provider/kernel/frontend or
Tier 1/2 CI-equivalent gates are not established by this proof. The known
unchanged whole-tools timeout is not rerun as reassurance.

## Remaining #24 scope

Review must establish whether this bounded validator is sufficient for the
agreed renderer profile before any renderer or CLI publication work proceeds.
Graph-to-document generation, pinned-C# witnesses, standard-consumer handling,
CLI check mode, exposure policy and evidence-backed product parity remain
unimplemented or unverified. `Generate` continues to refuse OpenAPI requests.
