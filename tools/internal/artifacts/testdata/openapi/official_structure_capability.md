<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

# Official-structure Stage 1 capability: blocked on URI format

This is an unfinished, test-only experiment for issue #24, not a production
renderer, an exported validator, or a general OpenAPI conformance claim. The
normative [OpenAPI 3.1.1 specification](https://spec.openapis.org/oas/v3.1.1.html)
wins over the informative structural schema and the engine.

The new route fixes the historical response-key false acceptance: both native
and actual QUERY operations reject `responses: {"wrong": ...}`. The full new
matrix nevertheless fails. `externalDocs.url: "not a uri with spaces"` is
accepted for both operation kinds despite format assertions being enabled.
Those two rejection assertions remain failing. Do not implement the renderer
until the full capability gate passes and its scope is reviewed.

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

## Matrix and stop result

The compiled Go 1.26.8 run exercised **326 leaf subjects** across 11 top-level
checks: **324 passed, 2 failed**. This is a failed gate, not a partial pass that
permits renderer work.

- Complete pointer sets match **16** numeric-fixture and **31** contextual-fixture
  Schema Objects, not merely a nonzero count. The contextual fixture includes
  all admitted nested schema positions, component parameters/headers/bodies/
  responses, callback, webhook, unused Path Item, native, QUERY, and encoding
  header schemas. Every compiled location retains the original decoded pointer.
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
- Original six numeric failures and original kin QUERY failure remain unchanged
  in separate nonzero runs. Their exact numeric/ownership subset still passes.
- **Blocker:** v6.0.3's `validateURIReference` delegates to Go `net/url.Parse`,
  which permits spaces in a relative path. The pinned official schema's
  `external-documentation.url` uses this format, so both the native and QUERY
  invalid URI controls are accepted. No assertion inversion, skip, alternate
  validator, engine upgrade, or hand-written format workaround was added.

Inventory budgets are 128 levels and 10,000 typed objects. This finite profile
is not a universal Schema Object/OpenAPI validator. No HTTP consumer, renderer,
CLI integration, precision codec, or generic media-range acceptance is proved.

## Reproduction

From `tools/`, use the actual Go 1.26.8 binary in `PATH`, `GOWORK=off`,
`GOTOOLCHAIN=local`, and the ignored tools-module `capability.mod` retaining
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

# Must pass before Stage 1 can advance; currently nonzero on two URI controls.
go test -mod=readonly -modfile="$CAPABILITY_MOD" -count=1 -timeout=90s -v \
  ./internal/artifacts/testdata/openapi -run '^TestOfficialCapability'
```

Only the explicit capability package was run. Broad root/tools/provider/kernel/
frontend or Tier 1/2 CI-equivalent gates were not run; this failed proof is not
release-ready. The package resides under `testdata` and is intentionally not
selected by ordinary `./...` runs.
