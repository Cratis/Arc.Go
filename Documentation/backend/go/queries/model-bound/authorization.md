---
title: Query authorization
description: Replace model authorization at the query level and suppress ordinary factories on denial.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Protect the model's query family once, then override a particular query when it
needs a different audience. Method authorization **replaces**, not combines with,
model authorization. Requirements within a declaration retain AND semantics;
roles within one requirement retain OR semantics.

## Supply declarations

Use `WithModelAuthorization` on `RegisterReadModel[M]`, and
`WithAuthorization[A]` on `Register[M]`. `WithDescriptor[A]` can pin the complete
method declaration. `AllowAnonymous` cannot combine with restrictions at that
same declaration level. Authentication schemes remain explicitly unsupported.

A supplied `authorization.Evaluator` must cover the complete catalog with exactly
matching declarations. Matching names alone is insufficient. Nil authorization
builds an evaluator with an empty policy registry: explicit authentication/roles
still run, and unknown named policies fail Build. With no declaration or configured
fallback, the existing authorization compiler treats an operation as public.

See [authorization declarations](../../authorization/index.md) for shared policy
and fallback semantics. Source directives are generator inputs, not runtime comments.

## Preserve factory staging

The pipeline enforces membership even on public queries. Declaration and custom
authorization filters run before ordinary filter and validator factories. Performer
factories/dependency resolution wait until ordinary validation succeeds.

Authorization is reevaluated immediately before invocation. Scope and prepared
security checks retain principal and tenant values **and their presence bits**.
A resource scope is not an authorization token. Adding an empty principal or tenant
to a context that originally lacked it is a security change, not a harmless default.

Denials are ready failures without data. A policy infrastructure error is denied
with safe exception diagnostics; it is not permission to continue. Query envelopes
do not expose a denial-reason field.
