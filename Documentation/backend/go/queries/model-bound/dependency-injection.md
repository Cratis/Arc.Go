---
title: Query dependencies
description: Supply repositories through closures or lazy operation resources after authorization and validation.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Business query methods can accept ordinary typed collaborators. Adapt those
parameters at composition time; caller-input fields never become dependencies
merely because a DI catalog contains their types.

## Capture a dependency

`queries.Function` accepts a closure. With your repository already constructed,
call it from that closure, as in the compile-checked
`ExampleFunction_closureDependency` in `queries/example_test.go`.
Shared captured collaborators must support concurrent calls.

This is the smallest container-free path. It declares no DI keys and requires no
resource opener. It is suitable for stateless repositories and application-owned
objects whose lifetimes already cover pipeline calls.

## Obtain operation resources lazily

Use `queries.Scoped` to adapt a concrete application holder implementing
`execution.Resources`. Use `queries.Invoke` when an adapter needs an expiring
`Invocation.Scope()`. `execution.ResourcesAs[T]` checks a holder's type;
`execution.Resolve[T]` obtains a service through its guarded resolver.

The performer-stage adapter runs only after declaration authorization, membership,
filters and argument validation. Invalid arguments and denied callers must not
construct performer dependencies. Factories return borrowed instances; the resource
holder owns their disposal. Do not retain an invocation, scope view or resolver.

## Use an optional DI provider

`PipelineOptions.ScopeFactory` adapts Fundamentals.Go's factory. A factory's
`Catalog` and `ScopeOwner` are optional capabilities in the published contract.
Supply `DependencyCatalog` separately if the factory does not expose it.
`WithDependencies[A](di.KeyFor[Repository]())` declares static requirements;
Build checks keys without resolving services. Missing services at execution remain
infrastructure errors, not ordinary validation rejection.

`OpenResources` and `ScopeFactory` are mutually exclusive. A borrowed DI scope
used with a configured factory requires `ScopeOwner` and must belong to it;
otherwise execution fails with `execution.ErrScopeOwner`. Plain custom holders
need only Arc's security/lifetime guard.

Continue with [pipeline ownership](../query-pipeline.md) for cleanup and cancellation.
