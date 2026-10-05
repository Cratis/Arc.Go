---
title: Publish an OpenAPI document
description: Write a deterministic OpenAPI 3.1 file for your commands and snapshot queries with arc-gen, check it in CI, and understand what it describes.
---

API consumers outside your frontend still need a contract: a partner team, a
gateway, or a contract test. Writing one by hand drifts the first time a command
gains a field. `arc-gen` can publish an **OpenAPI 3.1.1** document from the same
analysis that produces your Go adapters and TypeScript proxies, so the document
changes in the same commit as the code it describes.

The document is a **file only**. Arc.Go never serves it over HTTP and has no
embedded Swagger UI, Scalar or Redoc viewer. Host it yourself if you want one.

## Declare the profile

OpenAPI needs a `formatVersion` 2 profile, because the document states
assertions about your server that analysis cannot infer. Save this as
`arc-gen.json`:

```json
{
  "formatVersion": 2,
  "name": "Tasks",
  "defaultNamespace": "Tasks",
  "openapi": { "title": "Tasks API", "version": "1.0.0", "out": "api/openapi.json" },
  "server": { "runtime": "arc-go" },
  "responseFields": { "Cratis.ValidationResult": { "state": { "absent": true } } }
}
```

- `openapi.title` and `openapi.version` become the document's `info`.
- `openapi.out` is a `.json` file relative to the module root. The
  `-openapi-out` flag overrides it; an absolute flag value must name a file
  inside the module.
- `server` and `responseFields` assert how your host is configured. The current
  profile admits the builtin `arc-go` runtime without custom authentication,
  authorization policies or identity details, and validation results without a
  `state` member.

## Generate and check

From the application module:

```bash
GOWORK=off GOTOOLCHAIN=local arc-gen -config arc-gen.json ./features/...
GOWORK=off GOTOOLCHAIN=local arc-gen -config arc-gen.json -check ./features/...
```

The first command writes `api/openapi.json` and reports it as published. The
second writes nothing and fails when the committed document no longer matches
your source, so you can run it in CI. The output is byte-identical for the same
inputs regardless of package order.

`arc-gen` owns the file through a manifest. Without TypeScript output, the
manifest is `.arc-gen-manifest.json` in the module root; with
[TypeScript proxies](typescript.md) the document joins the manifest in the
TypeScript output root. Commit the manifest with the document. Both modes refuse
to overwrite a file the manifest does not own or a generated file you edited by
hand. When you remove `openapi` from a configured profile, the next run deletes
the previously owned document. Switching between the two manifest locations is
refused rather than adopted: move the existing document aside and regenerate.

## What the document describes

| Arc surface | OpenAPI representation |
| --- | --- |
| Command execute and validate routes | `post` operations with a required JSON body and the command or validation result envelope |
| Snapshot query (GET) | `get` and `head` operations; HEAD has no response body |
| Snapshot query (HTTP QUERY) | `x-cratis-query` path-item extension holding the operation, with a JSON request body |
| Query arguments | GET query parameters and members of the QUERY body's `arguments` object |
| Pageable results (`queries.Page[T]`) | `page`, `pageSize`, `sortBy` and `sortDirection` GET parameters and the QUERY body's `paging` and `sorting` objects |
| Models and enums | `components/schemas`, separately for input and output direction |

Each operation lists the 200, 400, 403, 413, 500 and 503 responses, plus 415 for
bodies, the correlation header and, for commands, `X-Allowed-Severity`. Integer
bounds are exact, including 64-bit values.

### Arguments

Arguments come from the query's argument struct and its `query` tags. Required
arguments (`query:"required"`) are required GET parameters. Missing, empty and
null values count as missing; `query:"default=…"` arguments bind their default.
Collection arguments use comma-separated form encoding (`style: form`,
`explode: false`), so elements cannot contain commas. The reader matches names
case-insensitively.

In the QUERY body, each argument accepts its JSON type or a text form parsed like
the GET value. Because names are case-insensitive, required arguments are listed
in `x-cratis-required-arguments` rather than a JSON Schema `required` list.

### QUERY is an extension

OpenAPI 3.1 has no QUERY method. The operation sits under the path item's
`x-cratis-query` member and is never presented as GET or POST. Standard
OpenAPI 3.1 tools see and route the GET and HEAD operations and ignore the
extension; only a Cratis-aware consumer can invoke QUERY from the document.

:::note[Schemas describe values, not tokens]
JSON Schema validates values. The reader also rejects some inputs a schema
accepts: duplicate (including case-variant) member names, trailing JSON, and
decimal or exponent spellings of integers such as `1.0`. Operation descriptions
state these boundaries.
:::

## Unsupported shapes refuse the whole document

`arc-gen` writes no document rather than an approximate one. Generation fails
with the reason when the selected packages contain any of these:

- Observable queries and other streams (document them separately).
- Authorization declarations, roles, discovery exclusions, custom block
  severities or portable validation rules.
- Field validation rules, opaque or declared codecs, UUID and date codecs,
  floating-point values (their codec admits special values), derived or
  polymorphic models.
- Query arguments that are models, maps, presence-preserving
  (`query:"preservePresence"`), validated, or use a reserved control name.
- Sortable read-model fields on a query whose result is not pageable.
- Graph diagnostics, `openapi.includeFrameworkEndpoints`, `openapi.streaming`
  set to `metadata`, or `openapi.servers` other than the relative `/` root.

## Deliberately not implemented

These are design decisions, not gaps waiting for a fix:

- **No HTTP exposure.** Arc.Go has no counterpart to C# `MapOpenApi()`; the
  document is not served at runtime and never changes with deployment state.
- **No embedded explorer.** There is no Swagger UI, Scalar or Redoc hosting.
  Serve the committed file with a viewer of your choice, behind your own access
  controls.
- **No security schemes.** Operations declare `security: []` and the document
  reports `securityVerified: false`. A document is never authorization.

## Compared with C# Arc

C# Arc's `MapOpenApi()` serves an OpenAPI 3.0 route catalog without body schemas,
and `Cratis.Arc.OpenApi` adds ASP.NET Core transformers. Arc.Go instead generates
an OpenAPI 3.1 file at build time with request and response schemas, and refuses
shapes it cannot describe. Response envelopes follow the C# HTTP contract; the
document itself is not a byte-for-byte port of either C# document.

## See also

- [Generate adapters and proxies](index.md) installs `arc-gen`.
- [TypeScript proxies](typescript.md) shares the profile and manifest.
- [Export Screenplay metadata](screenplay.md) uses the same graph.
