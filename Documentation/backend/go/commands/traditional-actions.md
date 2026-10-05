---
title: Traditional action handlers
description: Adapt an explicit POST handler to Arc command envelopes, with a validate-only route, request-value merging and a raw opt-out.
---

Some endpoints are not model-bound commands. A migration from a controller, a
webhook or a one-off administrative action may still want Arc's command result
envelope, validation findings and redaction without registering a command, a
`Provide` step or event consumers. `arc.NewActionHandler` adapts one such action to
an ordinary `http.Handler` that speaks the Arc command contract.

This is the Go translation of C# Arc's `CommandActionFilter` and
`FromRequestModelBinder` (Arc `7c1e780`). It is **Go-specific**: you construct and
mount the handler explicitly, and nothing is discovered from attributes.

## Mount an action and its validate route

`ExampleNewActionHandler` in `action_handler_test.go` is the complete compiled
example. The action receives a fresh, bound input and returns an `ActionResult`:

```go
type input struct{ Name string }
handler, err := arc.NewActionHandler(func(_ context.Context, value input) (arc.ActionResult, error) {
    return arc.ActionResult{Response: "Hello, " + value.Name}, nil
}, arc.ActionOptions[input]{})
if err != nil {
    return err
}
builder, err := arc.NewBuilder(arc.Options{})
if err != nil {
    return err
}
for _, route := range []string{"POST /greet", "POST /greet/validate"} {
    if err := builder.Handle(route, handler); err != nil {
        return err
    }
}
```

Posting `{"name":"Ada"}` to `/greet` returns status 200 and a command envelope whose
`response` is `"Hello, Ada"`. Run the example with:

```bash
go test -run ExampleNewActionHandler .
```

No route is registered for you: mount the same handler on the action path and its
`/validate` path. A request whose path ends in `/validate` (any letter case) binds
the input and runs validation, then stops. It **never** invokes the action or a raw
handler. Methods other than POST receive 405 with `Allow: POST`.

## What the adapter does and does not do

| Stage | Behavior |
| --- | --- |
| Construction | `T` must be a struct without methods. Invalid configuration fails before any callback runs |
| Input | An absent body binds a zero `T`; a present body must be one JSON object. Unknown members are ignored and duplicate declared wire names are rejected, as in Arc's serializer |
| Limits | `MaxBodyBytes` (1 MiB), `MaxQueryBytes` (8 KiB) and `MaxResponseBytes` (16 MiB) default like hosting options; oversized and malformed input never reaches the action |
| Validation | Optional `Validate` returns safe findings. By default only Error findings block; `TreatWarningsAsErrors` also blocks Warning, and a single `X-Ignore-Warnings: true` header overrides it |
| Action | Runs only for valid input on a non-validate path and an uncanceled request context |
| Publication | Success uses the command envelope with `response`. Zero and `false` responses are retained; a nil response is omitted |
| Failure | Errors, panics and blocking findings suppress both the response and any raw handler. Exception messages use production redaction regardless of the host environment |

The adapter consumes hosting correlation, identity and tenant context, but it is
**not** a command pipeline. It does not run `Provide`, catalog authorization
policies, operation scopes, transactions, response value handlers or event
consumers, and returned values are never interpreted as events or operations.
Protect the route with application middleware or an authentication handler, as
you would any `Builder.Handle` route.

## Merge request values into the body

C# Arc's `FromRequestModelBinder` fills a default body property only from a
nondefault request property. Supply `FromRequest` to opt in to the same rule. It
reads query or route values into a fresh `T`; it must not read the body. Return
malformed input as `*commands.DecodeError`; other errors are redacted.

This excerpt is adapted from `TestActionHandlerExplicitRequestMergeAndContext`,
with the options pulled into a variable:

```go
type actionInput struct {
    Count int  `json:"count"`
    Flag  bool `json:"flag"`
}

options := arc.ActionOptions[actionInput]{FromRequest: func(r *http.Request) (actionInput, error) {
    count, err := strconv.Atoi(r.URL.Query().Get("count"))
    if err != nil {
        return actionInput{}, &commands.DecodeError{Cause: err}
    }
    return actionInput{Count: count, Flag: true}, nil
}}
```

Posting `{"count":0,"flag":false}` to `/action?count=42` binds
`{"count":42,"flag":true}`: body zero and `false` are defaults, so the request
values fill them. The pinned fixtures in `action_request_test.go` record the rest:

- A nondefault body value wins over the request value.
- An explicit empty body string wins; a null plain string counts as missing.
- A non-nil scalar pointer in the body wins, even when it points to zero.
- Wire names match exactly. A differently cased member is unknown and ignored.

The merge profile admits flat built-in scalars and scalar pointers only. Custom
codecs, concepts, collections and embedded fields fail construction when
`FromRequest` is set. Use `*string` when null and empty must stay distinct.

## Return a raw response

Set `ActionResult.Raw` to an `http.Handler` to opt a successful action out of the
envelope. The raw handler owns status, headers, body and write errors through
ordinary `net/http` semantics. It runs after binding and must not retain the
writer. Returning both `Response` and `Raw` is an error, and a failed action never
reaches `Raw`. The adapter does not buffer or recover a raw handler after it may
have committed headers.

## Evidence

`action_handler_test.go`, `action_request_test.go` and `action_severity_test.go`
cover validate-never-invokes, rejection before the action, failed-response
suppression and redaction, zero/false/null responses, raw opt-out, limits and media
type, warnings and cancellation, request merging and invalid severities. These are
source-derived witnesses of the C# filter and binder specifications, not a paired
execution of ASP.NET MVC. See the [parity map](../../../parity.md) for the full
record and the [command pipeline](command-pipeline.md) for model-bound commands.
