---
title: Generator directives and signatures
description: Supported arc-gen declarations, method shapes, dependency stages, and diagnostics.
---

`arc-gen` is experimental tooling for typed command and snapshot-query adapters
and a bounded [TypeScript snapshot-proxy profile](typescript.md).
It analyzes explicitly selected packages with `go/packages` and `go/types`; it
never executes package initialization, constructors, or business methods.
Start with [generating model-bound adapters](index.md) for the authoring workflow.

## Command-line options

| Input | Default | Meaning |
| --- | --- | --- |
| Package patterns | `.` | Packages in the current module; dependency modules are rejected |
| `-dir` | Current directory | Directory for Go package loading |
| `-tags` | Empty | Comma-separated build tags, passed to the Go loader |
| `-check` | False | Return an error for missing, changed, or obsolete output without writing or recovery |
| `-config` | None | Strict format-version 1 JSON application profile |
| `-typescript-out` | Disabled | Override profile `typescript.out` with an output root relative to the selected module |
| `-emit-go` | True | Explicitly supplied value overrides profile `typescript.emitGo`; false is unsupported for mixed generation |

The Go environment also selects the build configuration. Generate and compile
with the same tags, GOOS, and GOARCH. The single output file represents that
configuration, not every possible artifact set. Regenerate when switching
configurations, or separate incompatible artifact sets into different packages.
Do not run concurrent generators against the same output paths.

## Declaration directives

Put directives in line comments attached to the declaration's documentation.
Options are whitespace-separated `key=value` tokens; values cannot contain
whitespace or quoted/escaped whitespace. A comma separates role alternatives.
Block-comment directives are rejected.

| Declaration | Directive | Options |
| --- | --- | --- |
| Package documentation | `//arc:namespace Shop.Inventory` | One logical namespace per package |
| Named command struct | `//arc:command` | `name`, `path`, `block-on` |
| Named read-model struct | `//arc:readmodel` | `name`, `path` |
| Query method or function | `//arc:query` | `model`, `name`, `path`, `http` |
| Artifact or query | `//arc:authorize` | Optional `roles=Editor,Admin` and `policy=CanWrite` |
| Artifact or query | `//arc:allow-anonymous` | No options |
| Artifact or query | `//arc:exclude-from-discovery` | No options |
| Type, method, or function | `//arc:ignore` | No options; cannot combine with other directives |

Names default to the Go type or method/function name. Namespace precedence is
package directive, builder namespace, then global. Import paths and filesystem
folders never determine public identity. Routes use Arc's existing metadata
algorithm. `path=` on a query explicitly disables a model-level path override.
`http` accepts only `GET` or `QUERY`.

`block-on` accepts `unknown`, `information`, `warning`, or `error`, matching the
runtime's inclusive validation floor. Unknown directives, misspelled options,
duplicate singular directives/options, invalid metadata, and misplaced directives
fail generation with file, line, and column diagnostics.

## Authorization and field metadata

Repeated `authorize` directives are AND requirements. Roles inside one directive
are OR alternatives. Bare `authorize` requires authentication. A query-level
declaration replaces model-level authorization; it does not merge with it.
Anonymous plus restrictions at the same declaration level is an error. Named
schemes are unsupported and diagnosed, never ignored.

Excluding a command or read model from discovery does not disable execution or
protect its endpoints. Read-model exclusion applies to all its generated queries.
Policy existence and dynamic registration compatibility remain builder checks.

Runtime field tags retain their existing behavior: `json`, `query`, `validate`,
`arc:"key"`, and `arc:"identity"`. Generated registrations use the public metadata
and binding APIs rather than another field-binding implementation. Do not combine
artifact directives with blank-field `arc` or `authorize` declaration metadata;
the generator rejects that ambiguity. Other directive families are untouched.

## Command methods

A command is a defined, nongeneric struct with a directly declared `Handle`.
Promoted methods are not discovered. The decoded command is the receiver; the
adapter never resolves a command instance through DI. Ignoring Provide omits that
stage. Ignoring Handle leaves an invalid command and is diagnosed. `arc:ignore`
cannot disable a runtime Validate convention; use manual registration options
when deliberately opting out of model validation.

| Method | Supported results | Parameter rules |
| --- | --- | --- |
| `Handle` | `(O, error)` or `error` | Optional context and command context; remaining parameters are exact dependencies or the provided payload |
| `Provide` | `(P, error)`, `(commands.Preparation[P], error)`, or a supported control plus `error` | Optional context and command context; other parameters resolve only during preparation |
| `Validate` | `([]validation.Result, error)` | Exactly `context.Context`; invoked by runtime model validation |

`context.Context` and `commands.CommandContext` are supplied by the invocation,
not DI. Each infrastructure type may appear at most once. Their positions do not
change the dependency stage.

Handle's receiver determines pointer or value registration. A pointer-only
Provide or Validate cannot accompany a value-registered Handle. The generator
diagnoses incompatible Validate signatures rather than silently skipping them.

A provided payload must match exactly one Handle parameter by Go type identity.
Zero matches or repeated matches are errors. Use a struct for multiple prepared
values. Typed nil payloads remain payloads; there is no assignability search,
null dropping, or fallback to DI for a missing preparation value.

Supported control-only Provide results are `validation.Result`,
`[]validation.Result`, `authorization.Decision`, and
`commands.Result[commands.NoResponse]`. The generated adapter uses an internal
empty payload and preserves the preparation-stage severity rules. Accepted
controls continue to Handle; `commands.StopProviding[P]` explicitly stops even
on success. Validation collections and command-result DTOs returned by Handle
remain ordinary values under the runtime return contract.

Provide and Handle dependencies have separate static manifests. Default resolution
uses `execution.Resolve[T]` only inside its callback. A pointer service is still
required; pointer syntax does not imply an optional DI registration.

## Read-model queries

The primary convention is an exported method with an unnamed or blank **value**
receiver on an `arc:readmodel` struct. It must return `(O, error)`, where `O` is the
owning model, its pointer, a slice/array of either, or `queries.Page[M]` or
`queries.Page[*M]`.

Unrelated return shapes and named/pointer receiver methods are not implicitly
discovered. Explicit `arc:query` on a stateful or pointer receiver is an error.
Use `arc:query` to select a private namespace method. A package-level query must
name a local opted-in read model through `model=GoTypeName`. Duplicate public
model/query identities fail instead of selecting a winner.

`context.Context`, `queries.QueryContext`, and `queries.Parameters` come from the
invocation. The first other parameter must be a named, nonpointer argument struct.
All subsequent parameters are exact service dependencies. With no caller input,
use `queries.NoArguments` before service parameters. If there are no ordinary
parameters, the adapter supplies a no-arguments registration automatically.

## Output and recovery

The following describes adapter-only generation. TypeScript-enabled invocations
use [manifest ownership and recovery](typescript.md#ownership-and-recovery) across
the complete Go/TypeScript plan instead; they never adopt legacy adapter files.

Each selected artifact package receives `zz_arc_generated.go` with a generator
version and format-version header, an `ArcBindings` type, and a
`RegisterArtifacts` function. No `init` registration or global registry is emitted.
`RegisterArtifacts` accepts one builder and zero or one binding sets. It stops at
the first registration error; registration is not transactional across artifacts.

Analysis overlays existing owned output with a package-only stub. This allows
clean bootstrap and recovery from stale methods, imports, and generated syntax.
Keep composition code that references generated symbols outside the selected
artifact packages. Handwritten inputs must type-check without generated symbols.

The generator validates all selected packages before writing. Each file replacement
is atomic, but a multi-package write is not a filesystem transaction. Any write
failure returns an error. After repairing the cause, rerun generation and `-check`.
An owned output is removed when its selected package no longer contains artifacts.
Files without the generator's ownership header, symlink outputs, and unselected
packages are never overwritten or cleaned up.

Variadics, unresolved generics, arbitrary multiple returns, streaming queries,
provider-specific query renderers, service constructor discovery, standalone
validator/policy discovery, operations, observable proxies, and proxy-only output
are unsupported. Supported TypeScript models, commands, and snapshot queries are
described in the [snapshot-proxy profile](typescript.md). Existing manually
registered model/concept validators remain supported.
A read-model declaration does not register a Chronicle projection.
