---
title: Execute a command
description: Execute, validate, or borrow an operation scope while preserving failure and response contracts.
---

Use the pipeline when a backend caller needs the same command boundary as other callers, rather than calling Handle and silently skipping its guards.

## Build and execute

`Registry.Build(PipelineOptions)` validates metadata, graph shapes, authorization declarations, route collisions, and declared DI keys without constructing application dependencies. Success freezes the registry; failure leaves it editable. The resulting `Pipeline` supports concurrent independent roots. Shared closures and extensions need their own concurrency contract.

Execution excerpt assuming a built pipeline and the Greet declaration from [model-bound commands](model-bound/index.md):

```go
result, err := commands.Execute[string](ctx, pipeline, Greet{Name: "Ada"})
```

Check the local `err`, then inspect `IsAuthorized`, `IsValid`, and `HasExceptions`. Expected validation findings and business denials may return a failed envelope with nil error. Infrastructure and cancellation errors remain inspectable with `errors.Is`/`errors.As`. `Response()` distinguishes absence from scalar zero.

The typed helper checks a known incompatible response contract before any application callback. Unknown contracts are checked against the actual response after execution; a mismatch returns `ErrResponseType` and a failed envelope without a response. A compatible typed call may still return no response.

## Follow the stages

1. Validate input, options, and registration; establish correlation and a fresh receipt without changing security presence.
2. Prepare authorization and tenant requirements; open or admit resources.
3. Install context values and resolve the command key; begin root participants.
4. Enforce membership, declaration authorization, and authorization filters.
5. Run ordinary filters and model/tag/registered/explicit validators.
6. Apply input severity; run Provide and its controls, then recheck security and invoke Handle.
7. Classify returns, update context, select one response, preflight controls, and consume effects.
8. Merge sticky nested failures; complete entered participants in reverse order.
9. Dispose owned resources and finalize once. Any failure removes response presence.

`ExecuteOptions` accepts zero or one value. Command severity floors cannot be loosened by the caller. [Returned warnings](validation-severity-filtering.md) have deliberately different semantics from input-stage findings.

## Validate without handling

`Validate` follows context, resources, membership, authorization, filters, and validation. It never activates completion participants, Provide, Handle, or response handlers. Its verdict is advisory, not a transaction or concurrency guarantee.

## Own or borrow resources

Unscoped Execute opens fresh resources through `OpenResources` or `ScopeFactory`; these options are mutually exclusive. A nil opener creates an empty guarded scope. `ExecuteScoped` and `ValidateScoped` admit a supplied `execution.Scope` and never close its resources. The caller must keep borrowed resources alive until execution returns. With a DI factory, borrowed DI resources must satisfy its explicit optional `ScopeOwner` capability; ownership is never inferred from resolver equality.

Callbacks receive expiring, non-closing scope views. Use `execution.ResourcesAs[T]` for plain holders or `execution.Resolve[T]` for resolver-backed holders. Generated adapters can call `Prepare` with separate stage manifests; manifests check the dependency catalog, not eagerly resolve dependencies.

## Handle failure honestly

Application panics become `execution.PanicError`. Production exception text is `An internal error occurred while processing the request. See server logs for details.` Development details require `ExposeExceptionDetails`; nil Logger means no automatic logging. Callback-supplied exception fragments are redacted too.

Completion and disposal use a cancellation-detached, cooperative budget (30 seconds by default). This keeps cancellation from hiding cleanup, but does not forcibly interrupt uncooperative callbacks. Failed response suppression is not rollback and does not prove an external write never committed.
