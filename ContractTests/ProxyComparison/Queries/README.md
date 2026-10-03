# Bounded Go-generated snapshot query family

`input.go.txt` is an analyzer declaration fixture, not an application. Its query
bodies and concept codecs are not executed. `Generated/` contains independent
Go renderer output, not copied C# proxies. Ordinary Go tests compare every byte
and the complete 13-file inventory: eight queries, Listing/Detail/Task models,
Status enum, and one barrel.

The internal `renderTypeScriptQueries(*Graph)` returns a complete sorted
snapshot/model/command family, or nil/error. Existing command and model renderers
retain their output. The public CLI still does not publish TypeScript.

## Contract and differences

Authority is Arc commit `7c1e78075b737df64f69fddfaae83374f75e3612`,
`Source/DotNET/Tools/ProxyGenerator/Templates/Query.hbs`, and the unchanged locked
`Snapshots/Primary22.48.2/ProxyComparison/All.ts` capture. Dependencies remain
Arc/Arc.React 22.48.2, Fundamentals 7.22.0, React 18.3.1 and TypeScript 5.9.3.

Single snapshots use `QueryFor<Model>` and `{}` defaults; slices, arrays and
`queries.Page[Model]` use `QueryFor<Model[]>` and `[]`. Paging is envelope metadata,
not a Page DTO or provider handle. Nullable single server results retain C#'s
model generic annotation; the runtime can return null. Plain collections do not
acquire automatic paging. Enumerable paging hook availability does not promise
backend paging support.

Parameter interfaces reflect binder requiredness independently of pointer
nullability. ParameterDescriptor's third argument is enumerability. Snapshot
parameters are plain properties: QueryFor does not have command-style
propertyChanged callbacks. Requiredness checks the perform argument object or
`query.parameters`; assigning `query.id` alone does not satisfy that check.
Descriptor-collected instance values take precedence over arguments during request
construction, as the actual runtime specifies. Optional scalar defaults stay on
the server: proxies leave all initial parameter values undefined and omit them.
String, finite safe-number and boolean defaults are supported without initialization;
rich/collection defaults, invalid values, and required/default combinations diagnose.

Routes come from finalized shared metadata.Resolve endpoints. FQNs remain logical
query identities regardless of routes/layout. Explicit Get/Query/Auto preferences
are checked against exposure. QUERY-only routes get an explicit Query preference;
unspecified preferences otherwise preserve Globals/runtime behavior. UI role hints
retain method-first stable union, not effective authorization evaluation.

**Approved Arc#2998 deviation:** sorting helpers use only the descriptor's declared
sortable result-wire fields, sorted deterministically, never query arguments.
The captured C# argument-based helper remains untouched. Comparable scalar/enum
fields are supported; unknown, duplicate, unregistered, nullable or complex fields
diagnose. An empty allowlist explicitly exposes no sorting helpers. Helpers merely
construct runtime Sorting or mutate query.sorting; they do not sort locally or
promise that an arbitrary provider applies sorting.

Static use/useSuspense/when and enumerable five-element paging hooks match the
pinned template's tuple and generic choices, including less-specific paged
PerformQuery. Imports use actual runtime subpaths and type-only erased bindings.
No mounted React or browser behavior is claimed.

## Reproduce

From tools, explicit fixture-only regeneration:

```sh
ARC_UPDATE_QUERY_FIXTURE=1 GOWORK=off GOTOOLCHAIN=local go test -count=1 -timeout=2m ./internal/artifacts -run '^TestTypeScriptQueryFixture$'
```

From ProxyComparison, use the unchanged exact lock and execute actual tsc output:

```sh
npm ci --ignore-scripts
./node_modules/.bin/tsc -p Queries/tsconfig.json --noEmit false --outDir ../../.ai-work/output/ts-query/tsc-standard
./node_modules/.bin/tsc -p Queries/tsconfig.legacy.json --noEmit false --outDir ../../.ai-work/output/ts-query/tsc-legacy
node --test --test-timeout=30000 Queries/queries.test.mjs
ARC_QUERY_DECORATORS=legacy node --test --test-timeout=30000 Queries/queries.test.mjs
```

Strict compilation uses skipLibCheck:false and verbatimModuleSyntax, real packages,
positive tuple/signature assertions and expected compile failures. The runtime
loader only resolves extensionless imports and the same locked packages; it does
not transform or bundle JS or provide substitute APIs.

Client-unit controlled fetch cases are explicitly labelled. They cover rich/nested
hydration, concept erasure, parameter metadata, requiredness, undefined defaults,
zero/false/list arguments, client options, GET/QUERY/Auto request construction,
paging/sorting, denial and missing/null/empty data behavior. They are not backend
conformance evidence.

For actual Arc taskboard All/ByID snapshot GET and QUERY round-trips, build the
existing host unchanged from the repository root, then test from ProxyComparison:

```sh
GOWORK=off GOTOOLCHAIN=local go build -o .ai-work/output/ts-query/taskboard-host ./ContractTests/taskboard/host
node --test --test-timeout=30000 Queries/roundtrip.test.mjs
ARC_QUERY_DECORATORS=legacy node --test --test-timeout=30000 Queries/roundtrip.test.mjs
```

The test starts and joins its bounded ephemeral loopback host, seeds state through
a real command and compares independent complete query envelopes. Fixture logical
FQNs are analyzer-owned; snapshot transport uses the actual literal taskboard
routes. This is not a cache/subscription FQN integration test, backend provider
sorting test, or paired .NET server test.

## Boundaries

Only per-type class snapshot output is included. Model renderer restrictions
continue to apply. Unknown wire results/providers, unfinalized endpoints/owners,
unsupported query validation rules, unsafe profiles, conflicting files/barrels,
and runtime-member collisions return no partial output. Portable query validators
require their own paired semantic corpus; arbitrary Go validators are not translated.

No observable output, transport redesign, filesystem publisher, CLI success mode,
new CI workflow, package/lock update, expanded C# capture, mounted React test or
whole-product parity claim is part of this family.
