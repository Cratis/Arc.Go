---
title: Routes and stable identities
description: Resolve Arc command and query paths from explicit versioned metadata.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Use one `metadata.Catalog` for route decisions so later runtime and generated
clients do not invent separate conventions. Set `Version: metadata.Version`;
unsupported versions fail rather than being silently upgraded. The foundation
resolves routes only. It does not register an HTTP handler.

## Identity is not a route

`TypeName{Namespace: "Acme.Tasks.Registration", Name: "RegisterTask"}` has identity
`Acme.Tasks.Registration.RegisterTask`. Give types stable logical names rather than
inferring them from Go import paths.

A query adds its method name to the read-model identity:
`Acme.Tasks.Listing.Task.All`. That fully qualified identity includes `Task`;
its conventional route does **not** include the model name.

## C# route conventions

Call `metadata.Resolve(catalog, metadata.DefaultOptions())`. Defaults are the
`api` prefix, zero skipped namespace segments, endpoint names included and HTTP
QUERY enabled. The zero `Options` value intentionally disables those defaults.

With `SegmentsToSkip = 1`:

| Identity | Method | Route |
| --- | --- | --- |
| `Acme.Tasks.Registration.RegisterTask` | POST | `/api/tasks/registration/register-task` |
| Same command, validate only | POST | `/api/tasks/registration/register-task/validate` |
| `Acme.Tasks.Listing.Task.All` | GET and QUERY | `/api/tasks/listing/all` |

C# kebab casing inserts a dash before **every uppercase character** after the
first, except immediately after an underscore. `GetURL` becomes `get-u-r-l`, not
`get-url`; underscores become dashes. Conventional paths are lowercased, repeated
slashes collapsed and trailing slashes removed. A prefix is not itself kebab-cased.

Disabling `IncludeCommandName` or `IncludeQueryName` omits the endpoint name only
when there is one artifact of that kind in the namespace **after skipping**.
Multiple artifacts restore names. Commands and queries form separate groups;
explicit-route artifacts still count in those groups, matching C#.

## Overrides and diagnostics

`Command.Path` overrides the whole command route. For queries, `Query.Path` (a
pointer) wins over `ReadModelPath`. A non-nil empty method path selects convention
instead of falling back to the model path. Nonempty explicit paths retain exact
casing; query paths also retain trailing slashes. The prefix is not added.

Only literal absolute paths are supported in this foundation. Relative paths,
wildcards, escaped paths, dot segments, whitespace and repeated explicit slashes
fail with configuration errors rather than guessing host-specific routing rules.
Every derived endpoint is checked too. Command paths `/create/` and `/` are
rejected because appending `/validate` produces repeated slashes. Use `/create`
or configure a non-root conventional command route instead.

`Resolve` rejects duplicate identities and case-insensitive method/path collisions,
including command validation routes. Inspect `*metadata.CollisionError` with
`errors.As` for the key and both owners. Results and collision owner ordering are
deterministic by identity, independent of registration order. Commands and queries
may share a path because their methods differ. No HTTP matching/case behavior is
implied by this preflight: HEAD, redirects and 405/Allow belong to hosting.

See [the executable example](../../../../example_test.go) and
[the compatibility ledger](../../../parity.md) for the deliberately stricter
collision/path behavior and the remaining metadata graph work.
