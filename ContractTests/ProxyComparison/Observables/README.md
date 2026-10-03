# Production-generated observable queries

`input.go.txt` and `profile.json` are inputs to the real `arc-gen` CLI. Go tests
publish adapters and this mixed model/command/snapshot/observable family through
the existing owned-output coordinator, then verify deterministic regeneration and
`--check`. `Generated/` is Go-generated output, not a new C# capture. Its manifest
is captured as fixture data; it does not own this fixture directory.

Authority: Arc `7c1e78075b737df64f69fddfaae83374f75e3612`,
`Source/DotNET/Tools/ProxyGenerator/Templates/ObservableQuery.hbs`, the unchanged
`Snapshots/Primary22.48.2/ProxyComparison/Observe.ts` capture, and the locked
Arc/Arc.React 22.48.2 declarations. Tools use independently fetchable runtime
`78ebbf8fdff8` and Fundamentals.Go v0.2.0, without a workspace or replace.

Supported source declarations are `Source[O]`, `CurrentSource[O]`, `*State[O]`,
and `*Subject[O]`, including aliases. Emissions normalize owning models, pointers,
slices, arrays, value Page and value ObservedCollection. Wrappers and change hints
are not client data. Concrete sources use ordinary interface assignment, retaining
CurrentSource behavior. Dependencies resolve after admission; adapters do not open
streams, start workers or dispose resources.

The first collection-client milestone requires a selected direct Go ID/Id field
serialized as `id`, with a supported nonnullable scalar. Other identity layouts,
nullable collection elements and unsupported sources diagnose before publication.
Identity tags are not client delta extractors. Collection shape does not set the
runtime's streaming-required `WithEnumerable` option.

## Evidence

Independent consumer tests compare manual and generated registration, normalized
return/emission/data types, argument metadata, factory laziness, invalid/denied
admission, current zero/nil/pending snapshots, EOF/failure/startup failure,
cancellation and join-before-disposal (including continued ErrJoinPending cleanup).
Production CLI tests exercise mixed publication, whole-profile failure without
writes, and observable-to-snapshot transitions. Existing publisher tests cover
ownership, collisions, stale removal and journal recovery.

`consumer.ts` compiles all observable hook tuples against the exact npm lock.
Single hooks project the runtime tuple; collection paging exposes four elements;
change-stream item types use the shared model planner. This is not mounted hook
or browser-runtime evidence.

From `tools`, explicit fixture regeneration:

```sh
ARC_UPDATE_OBSERVABLE_FIXTURE=1 GOWORK=off GOTOOLCHAIN=local go test -count=1 -timeout=2m ./internal/artifacts -run '^TestProductionCLIObservableMixedFamilyPublication$'
```

From `ContractTests/ProxyComparison`, after `npm ci --ignore-scripts`:

```sh
node node_modules/typescript/bin/tsc -p Observables/tsconfig.json --noEmit false --outDir ../../.ai-work/output/ts-observable/tsc-standard
node --test --test-timeout=30000 Observables/observables.test.mjs
node node_modules/typescript/bin/tsc -p Observables/tsconfig.legacy.json --noEmit false --outDir ../../.ai-work/output/ts-observable/tsc-legacy
ARC_OBSERVABLE_DECORATORS=legacy node --test --test-timeout=30000 Observables/observables.test.mjs
```

The runtime tests execute actual tsc output and locked packages, checking metadata,
sorting, inherited snapshot perform, required arguments and hydration against
controlled fetch responses. They do not substitute a handwritten observable proxy.
No generated transport or delta algorithm is emitted.

## Pending next checkpoint

Generated-registration Node fixture lifecycle and real default WebSocket-hub/Delta
round-trip evidence remain pending commit 3. No real generated subscription,
sharing/reconnect, mounted React or full observable-parity claim is made here.
