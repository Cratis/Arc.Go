---
title: Command authorization
description: Protect command declarations and tenant membership before ordinary dependency activation.
---

Reject callers before constructing their validators or handler dependencies. Command authorization is mandatory pipeline work; a custom authorization filter adds restrictions, never replaces the declaration.

## Declare access

`WithAuthorization[C](metadata.Authorization{...})` replaces the command declaration. Within one requirement, roles are OR; repeated requirements are AND. A nonnil empty declaration requires authentication. `AllowAnonymous` cannot coexist with restrictions at the same level. Named policies must exist when Build compiles the catalog; authentication schemes remain unsupported.

A nil evaluator does not bypass authorization: Build compiles the registry catalog with an empty authorization registry. Undeclared commands are public when that registry has no fallback. For a protected application baseline, supply an evaluator built with your fallback policy. The evaluator must cover every target with exactly matching declarations, not merely matching names.

## Understand activation order

Resource opening, context-value providers, key resolution, and root participant Begin are trusted preauthorization work. Keep those factories cheap and lazy. Membership then runs even for public commands, followed by declaration authentication/roles/policies and custom authorization filters.

Only an allowed command constructs ordinary filters, validation factories, or scoped handler adapters. Rechecks preserve principal and tenant values **and their presence bits**; a borrowed resource scope never grants permission.

## Keep tenant selection separate

`RequireTenant` rejects an unset tenant before opening resources. `Membership` independently decides whether the principal may act in the selected tenant. Neither selecting a tenant nor carrying a system principal bypasses the declared policy.

Business denial yields an unauthorized result. A policy infrastructure error yields unauthorized plus safe exception diagnostics, while the returned local error remains inspectable. Inspect `IsAuthorized`, `IsValid`, and `HasExceptions` separately; [pipeline execution](../command-pipeline.md) describes publication and cleanup.
