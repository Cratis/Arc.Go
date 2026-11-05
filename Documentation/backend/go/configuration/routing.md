---
title: Routes and stable identities
description: Resolve Arc Go command and query routes from versioned metadata catalogs, apply path overrides and inspect C#-compatible conventions without activation.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Use `metadata.Resolve` to turn the commands and queries in one `metadata.Catalog`
into HTTP method/path pairs without activation or I/O. This lets you inspect the
route decisions separately from starting a host. Set `Version: metadata.Version`
when constructing the catalog; unsupported versions, including an unset version,
fail rather than being silently upgraded. For a hosted application, builder
registration and `Build` perform the hosted validation. The builder uses the same
route resolution to compile the HTTP handler; resolving a catalog alone does not
validate the whole host.

Start with `metadata.DefaultOptions()` for the defaults below, or pass explicit
`metadata.Options` when you want different conventions. A zero-valued options
struct is not the default configuration: it has an empty prefix, omits names
unless needed to resolve namespace conflicts, and disables QUERY support.

## Identity is not a route

Keep identity separate from the URL when comparing resolved endpoints.
`TypeName{Namespace: "Acme.Tasks.Registration", Name: "RegisterTask"}` has identity
`Acme.Tasks.Registration.RegisterTask`. Give types stable logical names rather than
inferring them from Go import paths. Changing a prefix or override changes the
route, never that identity.

A query adds its method name to the read-model identity:
`Acme.Tasks.Listing.Task.All`. That fully qualified identity includes `Task`;
its conventional route does **not** include the model name. Use the read-model
namespace and query name to work out its path, as in the table below.

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

To work out a conventional path, the Go resolver skips the configured leading
namespace segments and applies C# kebab casing to the remaining segments and
endpoint name. Kebab casing inserts a dash before **every uppercase character**
after the first, except immediately after an underscore. `GetURL` becomes
`get-u-r-l`, not `get-url`; underscores become dashes. Conventional paths are
lowercased, repeated slashes collapsed and trailing slashes removed. The prefix
is not itself kebab-cased, though the conventional path is lowercased as a whole.

Disabling `IncludeCommandName` or `IncludeQueryName` omits the endpoint name only
when there is one artifact of that kind in the namespace **after skipping**.
Multiple artifacts restore names. Commands and queries form separate groups;
explicit-route artifacts still count in those groups, matching C#.

## Overrides and diagnostics

Set `Command.Path` when the whole command route should be explicit; the prefix is
not added. For a query, `Query.Path` (a pointer) takes precedence over
`ReadModelPath`. Leave the pointer nil to allow the model path, or supply a non-nil
empty method path to select convention instead. Nonempty explicit paths retain
exact casing; query paths also retain trailing slashes. Check which of these
inputs is set before changing the prefix to diagnose an unexpected URL.

Only literal absolute paths are supported. Relative paths, brace-delimited
route templates, escaped paths, dot segments, whitespace and repeated explicit
slashes fail with configuration errors rather than selecting host-specific
routing rules. Check the derived validation path as well as the command path:
`/create/` and `/` are rejected because appending `/validate` produces repeated
slashes. Use `/create` or configure a non-root conventional command route instead.

If resolution reports a collision, inspect `*metadata.CollisionError` with
`errors.As` for the key and both owners. `Resolve` rejects duplicate identities
and case-insensitive method/path collisions, including command validation routes;
it does not select the first registered performer. Results and collision owner
ordering are deterministic by identity, independent of registration order.
Commands and queries may share a path because their methods differ.

Use the resolved casing when sending a request: runtime matching is exact and
case-sensitive. Hosting adds explicit HEAD for GET, empty 404/405 with deterministic
Allow and noncanonical-path rejection without redirects. The
[HTTP contract](../reference/http-contract.md) describes those request-time rules;
resolver output is not the complete list of hosted behavior.

Follow [the executable example](../../../../example_test.go) to see the catalog,
options and resolved endpoints together. Use
[the compatibility ledger](../../../parity.md) for the deliberately stricter
collision/path behavior and the remaining metadata graph work, not as an
additional host-setup step.
