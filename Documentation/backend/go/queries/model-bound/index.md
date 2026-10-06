---
title: Model-bound queries
description: Share ordinary read-model structs between Arc queries and Chronicle projections without coupling registration.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Put query methods beside their read model so you can find state and selection
logic together. The model remains an ordinary named struct: no base type,
constructor, service-provider field or hidden framework state.

Use [unnamed namespace methods](static-methods.md) as the static-equivalent
convention. Directed top-level functions are equally supported. Register the
model only when you need shared identity, path or authorization overrides;
otherwise `queries.Register[M]` uses its type name and the registry namespace.

## Shared Arc and Chronicle rules

One struct can serve both frameworks. These are the agreed shared type rules,
not a claim that Arc executes Chronicle projections:

1. **Disjoint metadata.** Arc owns `arc:"…"` and `//arc:`; Chronicle owns
   `chronicle:"…"` and `//chronicle:`. Neither interprets the other's metadata;
   both tolerate other tag keys. Arc validates unknown options within its own tags.
2. **Explicit wire names.** Use explicit `json` tags. Queries, proxies, projection
   paths, read-model schemas and indexes must agree on the exact serialized name.
   Do not rely on different framework default naming policies, especially initialisms.
3. **Exported state only.** Only exported serializable fields participate.
   `json:"-"` fields are invisible to both and cannot carry Chronicle mappings.
4. **Pointer nullability.** `*T` means nullable/absent. `omitempty` and `omitzero`
   affect serialization only; they do not change projection semantics.
5. **Concepts.** Domain values can implement Fundamentals.Go `concepts.Concept[T]`.
   `concepts.Underlying` recognizes the scalar representation and `CheckJSON`
   checks codec shape. Plain named primitives need no extra marker. Query text
   binding uses `UnmarshalText`, not private concept state.
6. **Separate keys.** Chronicle's read-model key is `chronicle:"key"`, with
   `ID`/`Id` fallback. Arc identity is `arc:"identity"`; argument declarations
   remain separate. These declarations may reference the same field but neither
   implies a command key or a Chronicle event-source identity.
7. **Separate registration.** Register the same type independently with each
   framework: Arc registrations/adapters and Chronicle read models/projections.
   There is no shared global registry and no `init` registration.

This shared declaration excerpt uses primitives only:

```go
type Item struct {
    ID   int     `json:"id" arc:"identity" chronicle:"key"`
    Name string  `json:"name"`
    Note *string `json:"note,omitempty"`
}
```

Namespace methods are ignored by serialization and projection field inspection.
Chronicle owns FromEvent/SetFrom-equivalent declarations. Arc registration
implies neither projection ownership nor command-side model injection; projection
ownership must not require Arc registration or invoke query methods.

The [Chronicle integration](../../chronicle/index.md#one-model-two-frameworks)
includes a shared model registered with both frameworks and a kernel test that
projects events and serves the result through Arc. Query registration alone
demonstrates no persistence.

## Pin public identity deliberately

`queries.NewRegistry(queries.RegistryOptions{Namespace: "Shop.Inventory"})`
sets a logical namespace, never one derived from an import path or folder.
`RegisterReadModel[M]` accepts `WithModelIdentity`, `WithModelPath` and
`WithModelAuthorization`. Explicit query descriptors remain available for migrations.

For nongenerated declarations, a single blank metadata field can use
`json:"-" arc:"readmodel,name=Item,namespace=Shop"`; authorization uses the shared
metadata grammar. Type renaming changes the default public identity: pin it before
moving or renaming a model consumed by clients.

Continue with [namespace methods and functions](static-methods.md) or
[method-over-model authorization](authorization.md).
