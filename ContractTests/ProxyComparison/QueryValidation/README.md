# Generated snapshot-query string validation

This fixture covers `notNull`, `notEmpty`, `minLength`, `maxLength` and `length`
on snapshot query strings. The production `arc-gen` CLI emits `Generated/` and
registers `validation.NewPortable[SearchArguments]` with `queries.WithValidator`.
The independent Go consumer uses the tools module's fetchable Arc pin, without a
workspace or local replacement. `cases.json` drives both server and client checks.

## Contract boundaries

- GET and QUERY reject invalid arguments before the generated business method.
- Arc client 22.48.2 rejects the same findings before `fetch` for `perform(args)`
  and `query.parameters`: exact member names, messages, numeric severities and
  UTF-16 length boundaries. Warning and Info findings also block queries.
- Nullable optional strings omit length checks when missing or null. The builtin
  HTTP reader also treats **empty strings as missing**. The generated validator
  normalizes a copy for that binding behavior, without mutating the arguments or
  changing `validation/portable.go`. `notNull` therefore rejects an empty nullable
  HTTP string; `notEmpty` reports one finding, not an additional length failure.
- Requiredness is distinct from portable rules. Required arguments keep their
  generated interface and `requiredRequestParameters` contract. Malformed input
  and missing-required binding diagnostics are not portable-rule findings.
- Unproved rules, observable validation, server defaults and optional nonnullable
  rule-bearing strings are refused. Optional/concept/server-only rule flags and
  message substitution are not silently weakened. Refused production generation
  leaves all existing output bytes unchanged.
- The pinned client's descriptor-backed **instance fields override transported
  arguments after client validation**. The witness characterizes this limit;
  it does not alter upstream behavior or claim client validation is a security
  boundary. Server validation still rejects such input. No browser or mounted
  React-hook claim is made here.

Source authority: Arc `7c1e78075b737df64f69fddfaae83374f75e3612`,
`Source/DotNET/Arc.Core/Queries/QueryValidator.cs`,
`Source/DotNET/Arc.Core/Queries/Filters/FluentValidationFilter.cs`, its
`and_the_member_names_are_compared_with_the_client` specification, and
`Source/JavaScript/Arc/queries/{QueryFor,QueryValidator}.ts`. Executed client pins
are those in the parent `package-lock.json`; this is not a new paired C# host run.

## Run

From `tools/`, with `GOWORK=off` and `GOTOOLCHAIN=local`:

```sh
go test -count=1 -timeout=110s ./internal/artifacts -run '^TestQueryValidation'
```

To deliberately refresh the production CLI snapshots, set
`ARC_UPDATE_QUERY_VALIDATION_FIXTURE=1` for that test and inspect every change.

After preparing the parent's pinned frontend dependencies, from the repo root:

```sh
node ContractTests/ProxyComparison/node_modules/typescript/bin/tsc \
  -p ContractTests/ProxyComparison/QueryValidation/tsconfig.json \
  --noEmit false --outDir .ai-work/output/query-validation
ARC_QUERY_VALIDATION_COMPILED="$PWD/.ai-work/output/query-validation" \
  node --test --test-timeout=30000 \
  ContractTests/ProxyComparison/QueryValidation/validation.test.mjs
```

Repeat compilation with `--experimentalDecorators true` into a separate output
folder to exercise legacy decorators. The client suite requires its explicit
compiled-output path; it never substitutes a handwritten validator.
