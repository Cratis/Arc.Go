---
title: Start and stop application work
description: Register explicitly owned resources and drain HTTP and direct operations safely.
---

A canceled context signals work to stop; it does not prove work has ended. Arc
closes admission before draining callbacks, then stops resources only after the
callbacks have joined.

## Register ownership

`AddLifecycle(name, participant)` accepts a `Start(context.Context) error` and
`Stop(context.Context) error` implementation. Names are unique. Start follows
registration order; Stop follows reverse order and includes a hook whose Start
failed. No lifecycle callback runs under an application lock. Hooks must honor
context and join any workers they launch. Borrowed clients are never closed just
because they implement a closing interface.

## State and concurrent calls

The application moves from Built through Starting and Running to Stopping and
Stopped. Startup failure rolls back entered hooks and is terminal. Concurrent
Start callers join the same attempt; repeated successful Start is harmless.
A completed startup context does not become the application lifetime. Restart
after stop or failure is unsupported.

HTTP and direct calls through `app.Commands()`/`app.Queries()` share application
admission. Lookup remains available without starting. Nested commands stay owned
by the command pipeline, not a second application admission scope.

## Handle an expired shutdown budget

Shutdown cancels active callbacks when its caller's budget expires and returns the
context error. If callbacks have not joined, Arc remains Stopping. It does not
dispose shared resources or claim success; a later Shutdown can continue joining.
Concurrent callers have independent waiting budgets. Hooks own their cooperative
stop behavior, and no detached cleanup is launched to hide an unknown outcome.

Arc stops observable admission, cancels and joins observations and physical hub
connections (including hijacked WebSockets) before ordinary request drain and user
hooks. Failed source opening retains cleanup and its application admission lease
until workers actually join. See [observable lifetimes](../queries/observable-queries.md).

Operation resource `Close` still runs at most once. A holder that owns resumable
cleanup can explicitly implement `execution.ResourcesJoiner`: Close initiates
cleanup once and `Join(ctx)` waits for that same work without repeating disposal
side effects. Context errors mean an incomplete join; a recovered panic leaves
completion unknown. Later Close/Shutdown can retry only Join. A returned
non-context Join outcome certifies completion, even on failure. Non-context
failures and panic diagnostics from earlier attempts remain inspectable through
`errors.Is`/`errors.As`; only provisional canceled/deadline waiting errors clear
when joining finishes.
Plain holders without this capability have final, cached Close errors, including
context errors; they must not return while leaving unowned background cleanup.
`execution.ErrScopeJoinPending` distinguishes unfinished work from final failures.
Every pending **owned** `Scope.Close` result includes an inspectable
`*execution.PendingScopeError`, including after successful resource opening in
command/query pipelines or `RunWithResources`. Use `errors.As` to obtain it;
`Scope()` retains the closing scope so you can finish its Close with a fresh
budget. `errors.Is` still exposes `ErrScopeJoinPending`, context errors and other
cleanup diagnostics. Retain that scope while subsequent Close calls still report
pending; a final failure certifies completion but must still be handled.

If resource opening itself fails, OpenScope returns nil plus the pending error
when cleanup cannot join. Observable admission reclaims that scope automatically
for later Shutdown. Unary hosts do not automatically retain resource joins: your
host must retain the pending scope and resume Close, not merely check or log the
error. Do not opt a unary holder into asynchronous cleanup without that owner.
Borrowed scopes never transfer resource-disposal ownership through this error.

For embedded hosting, stop your external server as well. Arc owns its observable
connections, not arbitrary raw hijacks; track those in your own lifecycle participant.
