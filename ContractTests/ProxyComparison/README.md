# Proxy comparison reference

This test-only directory establishes the pinned C# reference for
[Arc.Go issue 20](https://github.com/Cratis/Arc.Go/issues/20).
It is **not yet a paired Go/C# proxy conformance suite**: Go TypeScript emission,
paired HTTP servers and rendered React hook tests remain to be added.

## Authority and attribution

- C# generator and client authority: Arc commit
  `7c1e78075b737df64f69fddfaae83374f75e3612`, NuGet/npm `22.48.2`.
- `reference/Fixtures.cs` is copied byte-for-byte from Arc.TypeScript commit
  `2f546a89c35d49726b78ed2bf573fff8d77b056e`, path
  `ContractTests/DotNET/ProxyComparison/Fixtures.cs`. Its SHA-256 is
  `0bc99230d0f89b556c7f5d90d725533252b9ace8d6a22ae411cfcc95197213a1`.
- `Snapshots/Historical22.45.0` retains that repository's original C# captures
  unchanged. These are historical evidence, not the current severity-policy
  template. `historical-provenance.json` retains its original provenance.
- The imported fixtures and snapshots are Copyright Cratis, MIT licensed; see
  the repository's [MIT license](../../LICENSE).
- `Snapshots/Primary22.48.2` is captured by executing the restored NuGet generator,
  never by hand-authoring C#-generated files. `provenance.json` pins the authority,
  source, ordered options, SDK, package hash, generator executable hash, project
  and lock hashes, and frontend npm integrity hashes.

## Reproduce the reference

Use Node `26.8.1`, npm `12.0.2`, and the exact .NET SDK in
`reference/global.json`. Run each command separately:

```sh
cd ContractTests/ProxyComparison
npm ci
cd reference
dotnet restore Reference.csproj --locked-mode
dotnet build Reference.csproj -c Release --no-restore
cd ..
npm run reference:check
npm run compile:reference
npm test
```

To deliberately replace the primary capture after reviewing changed inputs,
run `npm run reference:capture` instead of the check. Both modes require a
successful invocation of the pinned restored executable and exactly nine output
files. Only the C# timestamp is normalized; body hashes are validated and every
other byte is retained. Generation failure cannot fall back to old snapshots.
Set `AI_WORK_OUTPUT` to a task-owned scratch directory when running the generator;
otherwise small outputs are retained under the repository's ignored `.ai-work/`.

Strict TypeScript compilation uses untouched primary proxies and real packages,
with `skipLibCheck: false`. `baseline.consumer.ts` characterizes paging tuple
shapes, required Guid arguments and the upstream sorting defect. Runtime tests
exercise setters, revert, portable validator boundary values, nested model/Guid/
Date hydration, enums and the derived registry. They do not execute HTTP requests
or render React hooks and therefore do not establish backend or streaming parity.

## Approved sorting deviation

The captures intentionally retain C#'s argument-based `sortBy.id` helpers for
queries whose result model has no `id` field. Arc.Go's planned result-field
allowlist is the maintainer-approved correction for
[Arc issue 2998](https://github.com/Cratis/Arc/issues/2998). Do not edit C# captures
to conceal that defect. Once Go output exists, inventory each byte difference
with the exact pair hash and a formatting, approved API-deviation or upstream-
defect explanation.
