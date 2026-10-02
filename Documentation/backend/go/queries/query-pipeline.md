---
title: Query pipeline
description: Understand snapshot stages, operation-scope ownership and fail-closed publication.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Query execution separates caller binding from dependency activation. A malformed
argument or denied caller must not construct the performer's ordinary dependencies.
The pipeline owns snapshot publication and cleanup, not transactions or retries.

## Execution order

1. Resolve the registered logical name; establish correlation and a fresh receipt
   without changing principal/tenant presence.
2. Validate active paging and bind declared inputs atomically.
3. Prepare authorization, require the tenant if configured, and open/admit resources.
4. Install `QueryContext`; enforce tenant membership and declaration authorization.
5. Run authorization filters, then ordinary filters and argument-model validators.
6. Recheck authorization/security and invoke the performer.
7. Render, install the total, intercept items, and recheck cancellation/security.
8. Dispose owned resources and finalize once, retracting data and changes on failure.

All retained validation findings block queries, including information and warnings.
Snapshot results are ready; successful nil is ready-null. Failure never manufactures
pending state. Existing result constructors remain independent of pipeline finalization.

## Registries and extensions

`Registry` is a single-owner builder; its zero value uses the global namespace.
Successful Build freezes it, while failed Build leaves it editable. Build does not
activate performer, filter, validator, renderer or interceptor factories.

`AddAuthorizationFilter` and `AddFilter` append named factories. Filters contribute
verdict fragments only: data or change sets are rejected. A zero fragment denies;
continue with an explicitly authorized `NewResult` fragment without data.
Factories return borrowed instances, and shared instances must support concurrent calls.

`ContextFrom(ctx)` returns immutable query metadata: name, correlation, receipt,
principal, tenant, raw arguments, controls and the post-render total.
`Invocation.Scope()` exposes only an expiring, non-closing callback view.
No provider or execution owner is stored in context.

## Scope ownership

`Perform` always opens independent resources. Nil `OpenResources` opens a valid
empty scope. `PerformScoped` borrows compatible admission and never closes the
supplied scope. Sharing a command's resources does not enlist a query in command
completion or make the query a transaction participant.

Close stops new stage admissions and joins entered callbacks before disposal. A
query interrupted by scope shutdown fails rather than publishing partial data.
The implementation launches no goroutines. Callbacks are synchronous, must honor
cancellation and must not retain scope views or dependencies beyond their owner.

Cleanup uses a detached, value-preserving context with a cooperative bounded budget:
zero means 30 seconds; negative budgets fail Build. No detached cleanup goroutine
pretends to force a callback to stop.

## Errors and safe publication

Returned local errors preserve `errors.Is`/`errors.As` and joined cleanup causes.
Recognized validation failures become findings; validator infrastructure contracts
retain `validatorFailed` and `The value could not be validated.` Cancellation is
not validation. Application panics become `execution.PanicError`.

Production exceptions, including exception-bearing fragments, are redacted to
`An internal error occurred while processing the request. See server logs for details.`
`ExposeExceptionDetails` is an explicit development option. A nil logger performs
no automatic logging. Findings remain caller-authored safe text.

No data or change set survives denial, findings, exceptions, cancellation or cleanup
failure. That does not prove an external write rolled back or never committed.
See [result precedence](results.md) and [reader failures](using-the-http-query-method.md)
before mapping these outcomes to HTTP.
