# Paired snapshot HTTP checkpoint

This is a **Partial**, fail-closed checkpoint for
[Arc.Go#38](https://github.com/Cratis/Arc.Go/issues/38), not a parity approval.
It does not change runtime behavior or historical captures. A listening Kestrel
process is not proof that Arc registered its queries: readiness now requires the
four fixture performers and all eight GET/QUERY route mappings. An empty 404
still fails the paired gate.

## Reference and fixture

The authority is Arc source
`7c1e78075b737df64f69fddfaae83374f75e3612`, especially
`Source/DotNET/Arc.Core/Queries/QueryEndpointMapper.cs`,
`QueryableQueryRenderer.cs`, `BodyQueryRequestReader.cs`, and
`QueryStringQueryRequestReader.cs`. `prepare.py` reads that Git object, discovers
its six-project build graph, and extracts it to a new task-owned directory.
It never builds or edits the source checkout. The seventh project is the local
fixture. All seven dependency locks are committed; subsequent restores must use
`--locked-mode`. A NuGet Arc package is not a substitute for this source.

The fixture pins SDK `10.0.401` and both runtime patches to `10.0.12`, with
roll-forward disabled. `verify.py` checks the unchanged source, every restored
package/content hash in every project/target framework, fixture inputs, built
assemblies and actual runtime versions. Its task-owned output is rechecked before
the paired test starts either host. Source preparation alone does not prove HTTP
parity.

The fixture directly references `Arc.Core.Generators` as a private analyzer,
matching generated model-bound authoring in the pinned ASP.NET Core sample.
Arc.Core's private analyzer reference is not transitive; without the fixture's
own generated metadata, framework query metadata can suppress reflection
fallback and leave all four fixture queries unregistered.

Before advertising readiness, the C# host checks public
`IQueryPerformerProviders.Performers` for exactly four **Row** performers (not
the framework's global query count): Plain, Renderable, Filter and Failing at
`/api/plain`, `/api/renderable`, `/api/filter` and `/api/failing`. It checks
`IEndpointRouteBuilder.DataSources` for eight `RouteEndpoint` mappings, including
HTTP method and endpoint-name metadata: `ExecuteHttpConformance.Row.Method`
for GET and `QueryHttpConformance.Row.Method` for QUERY. Missing, duplicate or
wrong mappings fail startup before the readiness line, without HTTP polling.
The paired harness independently validates the advertised inventory.

The real C# host calls `AddCratisArc` and `UseCratisArc`, disables controllers,
enables QUERY, and explicitly redacts exception details. The real Go host uses
`arc.Application`, its built-in GET/QUERY readers, and the same exception policy.
Four queries return fresh immutable fixture membership: an ordinary list, a
renderable collection, a scalar/default filter, and a failing performer. Only
the renderable and filter queries opt into Go's existing `SliceRenderer` through
per-query `WithRenderer`. Ordinary lists remain the unpaged control.

## Corpus and comparison

`corpus.go` fixes 14 named groups and 36 requests, each executed against both
hosts. Every group includes GET and QUERY. The corpus covers baselines, ordinary
list paging, count-before-window ascending/descending sorting, empty selection,
out-of-range pages, scalar binding, missing/empty/null defaults, malformed page
and size, zero/negative size, invalid sort direction, and failure with paging.
GET's literal `null` is a string; JSON null belongs to QUERY.

The comparator checks status, relevant headers, fixed correlation echo, the
complete envelope, array order, paging, and missing versus null. It normalizes
only JSON object ordering and whitespace. It rejects duplicate object members,
trailing JSON, and lossy numeric comparisons. One existing Go behavior is explicitly
accepted: ordinary lists remain unpaged, including zero page/size metadata. The
`ordinary-list-unpaged` allowance applies only to `plain-paging-control/page/GET`
(`GET /api/plain?page=1&pageSize=2`, empty body) and
`plain-paging-control/page/QUERY` (`QUERY /api/plain`, exact body
`{"paging":{"page":1,"pageSize":2}}`). Only `$.paging.page` (C# 1, Go 0) and
`$.paging.size` (C# 2, Go 0) may differ. Both statuses must be 200, both totals
zero, and all four fixture rows must remain in order 3, 1, 4, 2. Headers and every
other envelope field remain strictly compared; failures never receive this
allowance. This preserves the documented ordinary-list behavior, not a new
paging implementation. New differences require explicit case/path-specific
approval.

Raw exchanges include the exact request, status, all response headers, body bytes
(base64 in JSON), and case-specific differences. Raw differences stay in each
capture, separate from allowance IDs/paths and unaccepted differences; an allowance
never rewrites a response or removes the raw diff. Each body is bounded to 1 MiB,
and combined host logs to 64 KiB. The hosts use ephemeral IPv4 loopback ports;
clients disable proxies and redirects. Readiness has 10 seconds, each request
3 seconds, graceful EOF shutdown 5 seconds, and kill/join 3 seconds. Forced
cleanup, missing prerequisites, partial inventory, partial execution and timeouts
fail, never skip. The Go host relaunches the compiled test runner, preserving
race instrumentation. The C# host launches its already-built DLL. Host logs and
joined process exit codes are retained alongside exchanges when an output path
is configured.

## Running

Native inventory/comparator/lifecycle checks need no .NET installation:

```bash
go test -race -count=1 -timeout=90s ./ContractTests/httpconformance
```

For the paired lane, allocate task-owned source, build and retained output paths
under ignored `.ai-work/`. Follow the explicit extraction, locked restore,
Release build, and verification steps in
[the dedicated workflow](https://github.com/Cratis/Arc.Go/blob/develop/.github/workflows/snapshot-http-conformance.yml).
Run heavy commands through the local bounded phase runner when using pi. Never
run restore/build inside the sibling Arc checkout.

After successful preparation, set absolute paths, prove activation, then run the
complete paired checkpoint (not a subtest selection). The activation regression
starts the actual generated host and plants missing-performer and missing-QUERY
observations in the same readiness verifier. Both must exit nonzero before any
readiness advertisement; no runtime handler is replaced.

```bash
export ARC_HTTP_CONFORMANCE_DLL="$PWD/.ai-work/httpconformance/output/artifacts/bin/Reference/release/Arc.Go.HttpConformance.dll"
export ARC_HTTP_CONFORMANCE_PROVENANCE="$PWD/.ai-work/httpconformance/keep/provenance.json"
export ARC_HTTP_CONFORMANCE_OUTPUT="$PWD/.ai-work/httpconformance/keep/exchanges"
go test -race -tags=httpconformance -count=1 -timeout=90s \
  -run '^TestReferenceActivation$' ./ContractTests/httpconformance
go test -race -tags=httpconformance -count=1 -timeout=110s \
  -run '^TestPairedSnapshotHTTP$' ./ContractTests/httpconformance
```

Commands, authentication, HEAD, CSV, renderer failures, databases, streams and
browser/React clients are deferred. This lane authorizes no release or PR.
