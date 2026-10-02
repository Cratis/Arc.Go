---
title: Authorization declarations and policies
description: Compile operation declarations and evaluate trusted identity before invoking application dependencies.
---

A validation warning must never bypass a permission check. Arc.Go separates
metadata preparation, role checks and policy evaluation so you can gate an
operation before constructing its validators or handler. These are foundations;
command/query pipelines and HTTP enforcement are not implemented yet.

## Declare operation requirements

Use keyed descriptor literals. The optional authorization fields extend metadata
format 1; additions to public structs can break unkeyed Go literals during v0.

| Declaration | Meaning |
| --- | --- |
| Nil | Undeclared; use application fallback, or public when no fallback exists |
| `&metadata.Authorization{}` | Authentication required |
| `AllowAnonymous: true` | Public; suppress fallback |
| Requirements | AND across requirements; OR within each role list |

A query method's `Authorization` replaces `ReadModelAuthorization`, rather than
adding to it. If both are nil, fallback applies. Commands use their own declaration
then fallback. Anonymous plus requirements is contradictory. Roles are exact,
case-sensitive strings; do not put comma-separated role syntax in a Go role list.

## Register and compile direct policies

Policies are borrowed concurrently-callable values or `PolicyFunc` closures, not
service registrations. This excerpt assumes a zero `authorization.Registry` named
`registry` and imports `authorization` and `context`:

```go
err := registry.Register("owner", authorization.PolicyFunc(
    func(_ context.Context, value authorization.Context) (authorization.Decision, error) {
        if value.Principal.ID() == value.Resource {
            return authorization.Allow(), nil
        }
        return authorization.Deny("not owner"), nil
    }), authorization.PolicyOptions{})
```

Inspect `err`, then call `registry.Build(catalog, options)`. Build validates all
supplied levels, even overridden read-model declarations, without calling any
policy. Queries sharing a read-model identity must supply identical
`ReadModelAuthorization` content, including declaration presence and requirement
order, even when a method overrides it. Mismatches fail with
`ErrInvalidConfiguration`. Build copies declarations and fallback. A successful build freezes the
single-owner registry; a failed build remains editable. Evaluators are immutable;
shared policies must independently support concurrent use.

Names are nonempty exact strings with no surrounding whitespace or control
characters. Empty roles, unknown/duplicate policies, invalid/duplicate artifact
identities, contradictory declarations and unsupported catalog versions fail.
Every named authentication-scheme declaration is rejected, matching native Core.
Route resolution is separate: future root Build must call both compilers.

Service-resolved policies and provider/scope checks are deferred while the
container integration is reconsidered. Use explicit constructor dependencies or
closures; do not capture request-bound identity or tenants in a shared policy.

## Prepare, evaluate and recheck

`evaluator.Prepare(ctx, target)` captures principal and tenant, including presence,
without authorizing or constructing anything. `prepared.Evaluate(ctx, resource)`
checks metadata continuity, then all role requirements, then policies in declaration
order. It checks metadata after each callback. `prepared.Check(ctx)` must also run
after other application callbacks and immediately before handler invocation.
A zero Prepared fails; it is never an authorization token.

Policies receive `Context` with Principal, Tenant, Target, borrowed Resource and
ReceivedAt. Do not mutate or retain Resource. Decisions are never cached across
operations or security identities. An anonymous caller can evaluate only when
every requirement is policy-only and every policy explicitly opts in with
`EvaluatesAnonymous: true`. Roles and empty authentication requirements prevent
that exception. Guest policies receive an empty Principal, never the anonymous
caller's ID, name, roles or claims. Continuity checks still compare the original
caller snapshot and presence, so replacing an anonymous caller or authenticating
during a callback invalidates the prepared operation. `identity.System` is not
privileged.

A zero Decision denies. `Deny(reason)` retains local diagnostics; `Decision.Err()`
wraps `ErrDenied` without exposing the reason in text. Policy errors return a denied
decision plus the original error. Cancellation remains cancellation. Unexpected
policy panics belong to future pipeline recovery/redaction boundaries.

Use `errors.Is` for `ErrInvalidConfiguration`, `ErrDuplicate`, `ErrFrozen`,
`ErrUnknownPolicy`, `ErrUnsupportedScheme`, `ErrUnknownTarget`,
`ErrIdentityChanged` and `ErrNotPrepared`; `ConfigurationError` supplies target
and policy identities. No claims or request values appear in configuration errors.
An unknown runtime target returns `ErrUnknownTarget` alone, not a configuration
error, and its error text does not include the requested identity.

Explicit anonymous bypasses declaration policies, not configured
[tenant membership](../tenancy/index.md). The evaluator does not create or verify
service scopes. Full pipeline enforcement remains pending in the
[parity ledger](../../../parity.md).
