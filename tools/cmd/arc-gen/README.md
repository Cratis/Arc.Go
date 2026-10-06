# Generate adapters and TypeScript proxies

`arc-gen` can generate Go registrations and supported TypeScript model, command,
snapshot-query, and declared observable-query families together. Go observable
adapters support declared Source/CurrentSource/State/Subject shapes with owning-model
emissions; opaque provider layouts remain unsupported. This is a partial frontend
compatibility profile for Arc/Arc.React **22.48.2** and Fundamentals **7.22.0**,
not full C# parity.
Node is needed for frontend verification, not for invoking the generator.

From the `tools` module, select your complete application artifact package scope:

```bash
go run ./cmd/arc-gen -dir /work/shop -config /work/shop/arc-gen.json ./features/...
go run ./cmd/arc-gen -dir /work/shop -config /work/shop/arc-gen.json -check ./features/...
```

Example profile (static namespace defaults are mandatory for proxies):

```json
{
  "formatVersion": 1,
  "name": "shop-frontend",
  "defaultNamespace": "Shop",
  "packageNamespaces": { "example.test/shop/features/tasks": "Shop.Tasks" },
  "routes": { "routePrefix": "/api", "segmentsToSkip": 1 },
  "typescript": {
    "out": "web/src/api",
    "clientVersion": "22.48.2",
    "proxyFileSuffix": false
  }
}
```

Call each generated package's `RegisterArtifacts(builder)` before `Build`.
Configure the builder with the same route options as the profile. Generated
adapters register endpoint expectations and a contract fingerprint through
`Builder.ExpectGeneratedEndpoints`; final runtime route drift fails `Build`,
including changes caused by additional manual artifacts. The API deployment base
path remains a frontend runtime setting, not a generator route prefix.

OpenAPI and Screenplay output also enable wire analysis and endpoint verification,
including `-screenplay-out` alone. They require static namespaces from source or
the profile. Adding or dropping the only wire consumer changes endpoint-bearing
Go adapters; changing only an output location does not. Use the same profile,
package/build scope and output flags for generation and `-check`. Omitting a
`-screenplay-out` flag used during generation can make the adapters stale even
when you are not checking the `.play` file.

## CLI and profile reference

| Flag | Default and meaning |
| --- | --- |
| `-dir` | Current directory; module/package loading directory |
| `-tags` | Empty; selected comma-separated Go build tags |
| `-config` | None; strict versioned JSON application profile |
| `-bindings-config` | Disabled; separate strict versioned constructor-service configuration |
| `-typescript-out` | Disabled; overrides profile `typescript.out` |
| `-emit-go` | True; an explicitly supplied value overrides profile `typescript.emitGo` |
| `-openapi-out` | Disabled; overrides profile `openapi.out` (module-relative `.json`); requires the profile's `openapi` section |
| `-screenplay-out` | Disabled; sets or overrides profile `screenplay.out` (module-relative `.play`); requires `formatVersion` 2 |
| `-check` | False; compare complete inventory and bytes without writes or repair |
| Package patterns | `.`; selected main-module application artifact packages |

Relative TypeScript output roots resolve from the selected module root.
`-emit-go=false` is diagnostic: proxy-only runtime contract verification is not
implemented. Without TypeScript output, the existing adapter-only invocation,
namespace defaults, generated-Go overlay and marker-based regeneration remain
compatible. The mixed-output manifest protections below apply to TypeScript-enabled
invocations; they do not retrofit a manifest onto legacy adapter-only output.

Profile fields:

- `formatVersion`: `1` retains the existing adapter/TS projection and fingerprints;
  `2` explicitly selects the richer [shared contract projection](../../internal/artifacts/contract.md).
  `name`: stable nonempty ownership identity.
- `openapi`: `title`, `version`, `out`, optional `servers` (only `/`). Requires
  `server` and `responseFields` assertions and publishes a file-only OpenAPI 3.1.1
  document; unsupported shapes refuse the whole document. There is no HTTP
  exposure or embedded viewer. See
  [Publish an OpenAPI document](../../../Documentation/backend/go/generation/openapi.md).
- `screenplay`: `out`. Publishes partial Screenplay 4.48.1 metadata; the profile
  `name` is the domain. There is no embedded viewer. See
  [Export Screenplay metadata](../../../Documentation/backend/go/generation/screenplay.md).
  Without TypeScript output, OpenAPI and Screenplay files are owned by a
  `.arc-gen-manifest.json` in the module root; with TypeScript output they join
  its manifest. Go adapters keep marker-based ownership in either case.
- `defaultNamespace` and `packageNamespaces`: logical names; source namespaces
  remain available. Reachable dependency models need a declared namespace or
  explicit package mapping; dependencies never receive generated Go adapters.
- `routes`: optional `routePrefix`, `segmentsToSkip`, `includeCommandName`,
  `includeQueryName`, `enableQueryHttpMethod`. Omitted options retain runtime
  defaults. Routes use `metadata.Resolve` once on the whole selected catalog,
  including discovery-hidden and frontend-excluded artifacts.
- `typescript`: `out`, `emitGo`, `segmentsToSkip`, `namespaceRoots`,
  `proxyFileSuffix`, `excludeTypes`, `excludeNamespaces`, `clientVersion`.
  Layout segment stripping defaults to routing stripping. Namespace roots are
  `{ "namespace": "Shop", "folder": "" }` entries; the longest exact/dot-prefix
  wins. Files use `.ts` or `.proxy.ts`, direct relative imports and sorted
  per-directory barrels, never recursive folder exports.
- `responses`: command FQN to `none` or `value` for otherwise ambiguous static
  returns. `commands.Outcome[T]` supplies a typed response contract. No dynamic
  response or effect-consumer discovery executes application code.
- `clientHttp`: query FQN to `Get`, `Query`, or `Auto`; preferences must agree
  with exposed methods. QUERY-only queries explicitly select `Query`.
- `typeRoots`: explicit compiler type identities to include.

Unknown JSON fields, invalid artifact override identities, unsupported versions,
unsafe roots/segments, case/path/barrel export collisions and unsupported layouts
fail the whole invocation. `sourceGrouping`, `interfaces`, `library` and custom
`imports` projection are not supported by this class-only publication profile.

## Supported boundary

Rich scalars and concepts use the shared compiler classifier, exact wire-field
kernel and real frontend constructors. Complete reachable model dependencies,
numeric enums, typed command responses and model-owned single/list/array/Page
snapshots are supported within the renderer's validated shape. Rules tags on
commands register `validation.NewPortable` in generated Go as well as the supported
client validator. Numeric/temporal precision limitations remain; generation cannot
repair the pinned serializer's null-write behavior.

Arbitrary interfaces, opaque codecs, unresolved dynamic responses, unsupported
rules/defaults/derived providers, rich dictionaries, eager constructor cycles,
nullable collection elements, unsupported query validation, channels, and unsupported observable
or opaque provider emissions fail instead of degrading to `any`. Snapshot query string
rules support `notNull`, `notEmpty`, `minLength`, `maxLength`, and `length`; they
register server validation and emit matching client validators. Observable-query
validation and unproved rule shapes are rejected. Primitive query
defaults are checked against the original Go grammar and target width, and remain
server-side defaults.
Query parameter names containing regex metacharacters are rejected for Arc 22.48.2
because its route helper interpolates keys without escaping
([Arc#3014](https://github.com/Cratis/Arc/issues/3014)). Sorting helpers use declared
sortable result fields, the approved Arc#2998 difference; their presence does not
invent provider-side paging or sorting. Declared observable proxies and bounded
mounted generated `use`, `useChangeStream`, and `useWithPaging` hooks have Node
coverage using explicit WebSocket hub/Delta providers. That is not browser/DOM,
StrictMode, every transport, or broader hook conformance; paging tuple coverage
does not prove provider-side windowing or totals.

## Ownership, check mode and interrupted publication

The output root contains `.arc-gen-manifest.json`: profile owner, selected package
and build-tag scope, contract fingerprint, relative Go/TypeScript paths and SHA-256
hashes. All analysis, rendering, layout and ownership preflight completes before
publication. Changing the scope while sharing a root is rejected; use a separate
root rather than accidentally cleaning another profile's files. Without TypeScript,
artifact publication has one owner and package/build scope per module-root
manifest. A configured run that drops artifacts removes stale files only when
that owner and scope match; unrelated adapter-only runs leave the manifest alone.

For TypeScript-enabled publication, existing files must already belong to the
manifest and match its hashes and generator markers. Even byte-identical marked
files and barrels are rejected without manifest ownership, including new entries
in an existing manifest. There is no automatic adoption or ownership migration.
Preserve and move existing files after review, or choose fresh consumer/output
roots. This includes legacy adapters created by adapter-only generation; the
adapter-only path remains available but does not establish mixed-output ownership.
Unowned barrels and edited generated files are errors. Stale files are deleted only with matching manifest hash and
marker. User files and directories are never blanket-deleted. Missing active files
can be regenerated; unchanged bytes retain mtimes. Symlink destinations/ancestors,
traversal, unsafe device-name segments and case collisions fail; mutations additionally
use rooted filesystem operations. Use a trusted workspace with one generator writer.

Renames are atomic **per file**, not across the Go and TypeScript directories.
Before changes, `.arc-gen-pending.json` stores the complete old/new manifest and
changed bytes. Any write/rename/delete/manifest failure returns failure and retains
this recovery journal. Rerun the original invocation with unchanged inputs to
validate all live bytes, roll back the interrupted set, and publish the complete
plan. If live bytes were edited, recovery stops without overwriting them: preserve
the journal and your edits, then restore the recorded old/new owned bytes before
rerunning. Never delete the journal to hide a failed publication. This is process-
interruption recovery, not a power-loss/durable filesystem transaction guarantee.

`-check` compares manifest inventory, markers and bytes, reports missing/changed/
stale/ownership conflicts, and rejects pending recovery. It never creates directories,
repairs journals or deletes outputs. A clean TypeScript-enabled CLI reports the
adapter/file counts, profile and fingerprint only after complete success.

## Optional constructor services

Use `-bindings-config` when you want generated constructor registrations rather
than hand-authored DI factories. Follow the
[constructor-service guide](https://github.com/Cratis/Arc.Go/blob/main/Documentation/backend/go/generation/constructor-services.md)
for prerequisites, composition excerpts, and the configuration reference.
The option does not construct command DTOs, change wire schemas, or make a
container mandatory. Without this flag, ordinary generated
bytes and stage-local `ArcBindings` callbacks remain unchanged.

```json
{
  "formatVersion": 1,
  "package": "example.test/shop/services",
  "matchIFoo": false,
  "interfaces": [{"service": "IFoo", "implementation": "*Foo"}],
  "existing": [{"key": "*Settings", "lifetime": "singleton"}],
  "duplicates": "reject",
  "requireAllDependencies": true
}
```

`package` is the **one output owner**, an exact import path among your selected
main-module patterns. Omit `constructors` to use the shared planner's exact
package-level `NewX` convention across selected packages; an explicit `[]`
selects none. A nonempty list selects exact function references instead. There
is no recursive import discovery, richest-constructor choice, automatic pointer
conversion, or execution of application initialization or configuration code.

References use local `Name` or fully qualified `import/path.Name`, with an
optional `*` for a type key. Qualified references address selected packages and
their direct compiler imports, never a package loaded just for configuration.
Aliases retain exact Go type identity. Name closed generic/composite keys through
an accessible alias; expressions and open generics are unsupported. Foreign
constructors must be in the selected package universe, and their signatures must
be accessible from the owner. Marked command DTO constructors are rejected.

The shared `bindingtypes.Analyze` and `ReadDirectives` own selection, signatures,
structural ambiguity and `//cratis:singleton`, `//cratis:scoped`, and
`//cratis:ignore-convention` type directives. Default lifetime is transient;
same-package `IFoo` matching is opt-in. Explicit interface pairs choose an exact
concrete key and use borrowed forwarding with its effective lifetime. Errors and
informational obligations retain the shared `BT` diagnostic codes. Missing
non-scalar dependencies are informational unless `requireAllDependencies` is
true; scalar configuration always needs an explicit provider attestation.

Each `existing` entry requires an exact `key` and its **actual lifetime**:
`singleton`, `scoped`, or `transient`. This is your composition attestation, not
lifetime introspection. Registration preflights presence through optional
`di.Catalog` without resolution. Without Catalog, composition must guarantee
presence. The default rejects generated/existing overlaps; `keepExisting` retains
only explicitly listed keys and emits no registration for them. Other duplicate
or registrar errors propagate immediately: discard partial composition after an
error. Repeating registration is not idempotent. For two generators, give one
ownership and tell the other those exact keys and lifetimes. Manually bind
external values with `di.BindValue` (borrowed singleton).

Call the owner's generated `RegisterServices(di.Registrar)` separately from
`RegisterArtifacts(builder)`, then build your provider and application. Neither
registration nor Build activates constructors. Zero-argument factories use
`di.Bind`; one through four arguments use the safe `BindFunc1`–`BindFunc4`
adapters. Larger supported constructors resolve ordered arguments individually
and declare unique direct edges. Disposable value results above arity four are
rejected. Context, exact result shape and failed non-nil result ownership are
preserved; borrowed forwarders and callbacks never own their returned values.
Constructor edges do not become every command's stage dependency manifest.
The container's Build remains authoritative for missing edges, cycles and
singleton-to-scoped captures.

Services share `zz_arc_generated.go` and its existing writer/preflight and mixed
ownership protections. Every selected package is analyzed and rendered before
publication. Service-only packages emit real bindings; removing the opt-in
removes selected stale owned output when no artifacts remain. No second service
manifest or writer is introduced. This is a bounded authoring checkpoint, not
full service-discovery, policy/validator parity, or Chronicle generator support.
