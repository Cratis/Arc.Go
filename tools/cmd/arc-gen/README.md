# Generate adapters and TypeScript snapshot proxies

`arc-gen` can generate Go registrations and supported TypeScript model, command,
and **snapshot-query** families together. This is a partial frontend compatibility
profile for Arc/Arc.React **22.48.2** and Fundamentals **7.22.0**, not full C# parity.
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

## CLI and profile reference

| Flag | Default and meaning |
| --- | --- |
| `-dir` | Current directory; module/package loading directory |
| `-tags` | Empty; selected comma-separated Go build tags |
| `-config` | None; strict versioned JSON application profile |
| `-typescript-out` | Disabled; overrides profile `typescript.out` |
| `-emit-go` | True; an explicitly supplied value overrides profile `typescript.emitGo` |
| `-check` | False; compare complete inventory and bytes without writes or repair |
| Package patterns | `.`; selected main-module application artifact packages |

Relative TypeScript output roots resolve from the selected module root.
`-emit-go=false` is diagnostic: proxy-only runtime contract verification is not
implemented. Without TypeScript output, the existing adapter-only invocation,
namespace defaults, generated-Go overlay and marker-based regeneration remain
compatible. The mixed-output manifest protections below apply to TypeScript-enabled
invocations; they do not retrofit a manifest onto legacy adapter-only output.

Profile fields:

- `formatVersion`: exactly `1`; `name`: stable nonempty ownership identity.
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
nullable collection elements, query validators, and observable/channel/provider
results fail instead of degrading to `any`. Primitive query defaults are checked
against the original Go grammar and target width, and remain server-side defaults.
Query parameter names containing regex metacharacters are rejected for Arc 22.48.2
because its route helper interpolates keys without escaping
([Arc#3014](https://github.com/Cratis/Arc/issues/3014)). Sorting helpers use declared
sortable result fields, the approved Arc#2998 difference; their presence does not
invent provider-side paging or sorting. Observable proxies and mounted React/browser
conformance are outside this profile.

## Ownership, check mode and interrupted publication

The output root contains `.arc-gen-manifest.json`: profile owner, selected package
and build-tag scope, contract fingerprint, relative Go/TypeScript paths and SHA-256
hashes. All analysis, rendering, layout and ownership preflight completes before
publication. Changing the scope while sharing a root is rejected; use a separate
root rather than accidentally cleaning another profile's files.

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
