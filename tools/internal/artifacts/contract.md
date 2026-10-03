# Shared contract projection v2

This internal reference describes the analysis checkpoint for
[Arc.Go#24](https://github.com/Cratis/Arc.Go/issues/24). It does not enable OpenAPI
CLI output, document hosting, JSON publication or an OpenAPI dependency.
`Generate` rejects an `openapi` request before loading or writing application files.

## Compatibility

Existing profile-v1 adapter and TypeScript invocations retain the v1 graph
projection and fingerprints, including the strict publication fixtures. Existing
TS optionality, cycle, enum-width and symbol admission rules are unchanged.
Profile-v2 opts into the enriched projection; a v1 profile cannot silently acquire
server, OpenAPI or schema assertions. V2 without `openapi` still supports the
existing Go/TS families.

`buildGraph` remains the only application graph. It uses the shared
`modelshape.Select` compiler adapter and `metadata.Resolve`, with compiler
attachments kept outside serialized contracts. No application marker, codec,
validator, constructor, handler or policy is invoked. The runtime authorization
registry validates declarations without policy evaluation.

## Value, property and binding metadata

`WireType.Kind` remains the TS-compatible kind. Its v2 `Contract` retains:

- Original declared identity, pointer depth, concept identity and exact scalar
  representation. Integer bounds are decimal strings, never float64 values.
  Target compiler sizes determine `int`/`uint` width. Float widths and builtin
  `NaN`, `Infinity`, `-Infinity` representations are retained.
- Fixed-array lengths, including zero, distinct from slices. Byte arrays retain
  numeric array shape, not inferred UUID. Their JSON input zero-fills/truncates
  through `encoding/json`; other fixed arrays require exact input length.
- Input/output value nullability and `missing-null-value` Optional representation.
  Shared UUID/date/local-time/.NET-TimeSpan identities retain their string
  representations and formats, regardless of primitive backing types.

`FieldDescriptor.Optional` remains a **client hint**, not JSON requiredness.
`Presence` describes fresh-zero/missing input, null acceptance, nil/missing output
omission, effective `omitempty`/`omitzero`, and nullable embedded parents.
For example, a nonempty fixed array with `omitempty` still has a required output
property. Required framework arrays are distinct from nullable application slices.
Nil omission applies to the immediate property value: a nonnil
`*Optional[int]` containing `Null[int]()` and a nonnil `**int` with a nil
pointee still publish explicit null. A missing Optional is omitted only when
the property itself implements Optional's presence hook (value or single pointer),
not through an additional pointer layer.
Output-only framework contracts explicitly retain array presence and envelope
payload omission; they are not a complete streaming-protocol schema.

`Binding` records builtin query reader behavior. Required/default flags do not
come from pointer shape. GET joins repeated values and accepts scalar text/CSV;
QUERY also accepts JSON arrays. Query binding retains exact fixed-array length
(including byte arrays), separately from JSON codec length behavior. Text codecs
such as UUID take precedence over their underlying collection representation.
Empty/null values normally become missing, while
`preservePresence` retains them. Required fields fail when missing; defaults retain
the original text, including full-width integers. Custom scalar defaults and
unsupported binder shapes diagnose the missing declaration boundary.

V2 normalizes runtime metadata before a separate TS capability walk of this same
graph. `TSIncluded` records that selected closure. TS exclusions do not erase API
operations or schemas; API-only nodes are not accidentally admitted to TS output.
Selected TS roots still reject Optional, rich dictionary hydration, constructor
cycles, unsafe enum/flags constants and reserved symbols.

Derived metadata records the default model without a discriminator and each
nondefault concrete ID. A concrete nondefault input can omit its discriminator;
its output includes it. These are codec facts, not overlapping `oneOf` branches.
Enum members/flags remain descriptive: they do not close the underlying numeric
domain. Validation rules retain exact raw arguments and severity metadata, not
stronger unconditional schema assertions.

## Explicit profile assertions

Profile-v2 adds:

- `openapi`: required `title`/`version`; optional `out`, `servers` (default `/`),
  `includeFrameworkEndpoints` (default false), `streaming` (`error` by default,
  or `metadata`). These select internal analysis, not a CLI renderer.
- `server`: required `runtime:"arc-go"`; `environment` (default Production,
  exactly Development selects development behavior); optional `http`,
  `authentication`, `authorization`, `introspection`, `identity`.
- `server.http`: builtin `queryReaders` only; correlation header and body/query/
  response limits, defaulting to `X-Correlation-ID`, 1 MiB, 8 KiB and 16 MiB.
- `server.authentication`: named `schemes` and ordered `handlers` referencing
  them. The bounded scheme subset is `http`, `apiKey`, `openIdConnect`; OAuth
  flows and other fields diagnose unsupported declarations rather than guessing.
- `server.authorization`: a `metadata.Authorization` fallback and named policies,
  each requiring an explicit `evaluatesAnonymous` boolean. Unknown policies and
  unsupported scheme-restricted requirements fail the runtime static validator.
- `server.introspection`: pointer-valued `enabled`/`requireAuthentication`, plus
  roles. Contradictory roles/public exposure or unenforceable authentication fail.
  Disabled catalogs do not imply disabled identity routes.
- `server.identity.detailsType`: exact declared Go type key. Custom details need
  explicit directional wire schemas; omitted details use the builtin null view.
- `wireSchemas`: exact Go type keys mapped to `input` and `output` schema objects.
- `responseFields`: operation/framework identities and supported opaque paths,
  declaring exactly one of `absent:true`, `type`, or `schema`.

`Assertions.Provenance` is `application-profile-assertion`. Generated endpoint
expectations verify routes, **not authorization or schema truth**. Server locations
are deployment assertions and never feed route resolution. HTTP schemes are not
inferred from roles/policies, and roles are not OAuth scopes.

Opaque custom codecs, including application concepts, need explicit directional
schemas; nominal scalar identity alone does not prove codec acceptance. An unknown
command response needs an existing response override or explicit `responseFields`
declaration. Known no-response and typed responses reject contradictory overrides.
Validation `state` requires an explicit shared declaration, even when absent.
Its framework field retains the analyzed `WireType`, including named scalar
widths, signedness, exact bounds and directional nullability; a target string
alone is not a scalar contract. The validation envelope omits every encoded
null (`Presence.OmitNull`), including nonnil codec values, so nullable state
values do not make the published property nullable.
TS import mappings are not schema evidence or proof of codec behavior.

Schema assertions preserve raw numeric tokens and require explicit root `type` or
`type` union. Empty schemas, external/local unresolved references and reference-only
or composite-only shapes are diagnostics in this checkpoint. Full schema validity
and instance validation belong to the renderer checkpoint. Dynamic application
`IsZero` predicates require a containing-type schema instead of guessed omission.
This is deliberately not a general schema DSL or automatic custom-codec inference.

See [source-derived expectations](testdata/wire-contract/README.md) for evidence
and capture distinctions. The OpenAPI parity status remains Partial.
