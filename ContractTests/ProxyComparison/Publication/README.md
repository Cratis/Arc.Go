# Production CLI publication fixture

These are **Go-generated** model, command and snapshot-query fixtures, not C#
captures. `tools/internal/artifacts/production_cli_test.go` runs the actual
`arc-gen` executable on the authored `input.go.txt` and dependency `shared.go.txt`,
compares all eight TypeScript/barrel files plus their mixed-output manifest, and
compiles/runs generated Go registration against the tools module's fetchable Arc
pin. The generated contract test accepts `/proxy` and rejects `/wrong` at `Build`.
Unsupported additional source must leave adapters, proxies and manifest unchanged.

The dependency supplies a shared concept, enum and nested rich-scalar model.
Generation never executes its codecs or application methods. Portable command
rules register on both sides. No dependency Go adapter is emitted.

## Verify actual CLI output with the locked frontend runtime

From repository root, with existing locked ProxyComparison dependencies installed:

```bash
node ContractTests/ProxyComparison/Publication/prepare.mjs
cd tools
GOWORK=off GOTOOLCHAIN=local go run ./cmd/arc-gen \
  -dir ../.ai-work/output/ts-publication/consumer \
  -config ../.ai-work/output/ts-publication/consumer/profile.json .
cd ../.ai-work/output/ts-publication/consumer
GOWORK=off GOTOOLCHAIN=local go build -mod=mod -o ../fixture-host ./host
cd ../../../../ContractTests/ProxyComparison
./node_modules/.bin/tsc -p Publication/tsconfig.json
node --test --test-timeout=30000 Publication/runtime.test.mjs
node --test --test-timeout=30000 Publication/route-helper.test.mjs
```

`prepare.mjs` refuses an existing scratch directory rather than deleting recovery
or user work. Outputs stay under repository-root `.ai-work/output/ts-publication`.
Run the phases separately; compilation needs successful CLI generation, and runtime
needs both successful Go host and TypeScript compilation. The TypeScript config
uses strict/no-skipLibCheck/verbatim compilation with real locked declaration paths,
not ambient substitutes. The Node loader only resolves locked runtime modules and
extensionless emitted JS; it performs no bundling, transpilation or serialization.

The real loopback host uses generated adapters at the tools runtime pin. Runtime
tests check manifest hashes and inventory, validation, command execution, complete
actual success envelopes, GET and HTTP QUERY snapshots, nested class/Guid/Date/enum
hydration, and independent direct server rejection of an empty title. Readiness,
request deadlines, shutdown and process joining are bounded. These tests do not
claim mounted React hooks, observable transport, browser behavior, provider-side
paging/sorting, paired .NET execution or full frontend parity.

`route-helper.test.mjs` deliberately hand-authors an invalid query to demonstrate
that the exact Arc 22.48.2 helper rejects `a[` before fetch for both methods. The
analyzer-backed Go regression rejects generating that contract. Its source at
`7c1e78075b737df64f69fddfaae83374f75e3612` and inspected upstream main
`e722789124e841c5011fe08b37820f8ba4e0a02e` share the unchanged
`UrlHelpers.ts` blob `802f0991332ad0f3cd8fb314a4fdd0bf49a31281`.
[Arc#3014](https://github.com/Cratis/Arc/issues/3014) tracks the fix; no fixed release
is claimed. Existing C# captures and npm manifests/locks are untouched.

For intentional snapshot updates only, from `tools`:

```bash
ARC_UPDATE_PUBLICATION_FIXTURE=1 GOWORK=off GOTOOLCHAIN=local \
  go test -count=1 -timeout=2m ./internal/artifacts \
  -run '^TestProductionCLIPlansPublishesAndChecksActualRuntimeContract$'
```

Ordinary Go tests never require Node and never silently skip a frontend gate.
