---
title: Command results
description: Construct Arc command outcomes with safe response presence and standard status selection.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

A command result tells a caller whether an action was authorized, valid and free
of exceptions. It is not an instruction to execute a handler. Result constructors create values only; the pipeline and HTTP application execute
and publish them separately.

## Construct an outcome

Use `commands.Success(correlationID)` for no response, or
`commands.WithResponse(correlationID, value)` for a typed response. The generic
`commands.Result[R]` exposes `Response() (R, bool)` so absence cannot be confused
with a valid zero value. `commands.NoResponse` names the no-response type argument.

To compose failure metadata, use `commands.NewResult(details, response)` with
`commands.Details` and `serialization.Optional[R]`. Set `Authorized` deliberately:
the zero value denies authorization. Any final failure discards the response.
No handler, scope, redactor or validation filter runs during construction.

Result constructors copy framework-owned slices, finding member lists and reason
details. Response values and application validation state are borrowed; keep them
immutable while sharing a result between goroutines. `Details()` returns new
framework-owned slices, not mutable access to the stored ones.

## Wire contract

The envelope always includes `correlationId`, `isSuccess`, `isAuthorized`,
`isValid`, `hasExceptions`, `validationResults`, `exceptionMessages`,
`exceptionStackTrace` and `authorizationFailureReason`.
Required arrays are `[]` and empty strings remain present. `response` is omitted
when absent or nil, but `0`, `false` and `""` remain present on success.

- `IsValid()` means the retained validation list is empty, regardless of severity.
- `HasExceptions()` means the exception-message list is nonempty.
- `IsSuccess()` combines authorization, validity and absence of exceptions.

`StatusCode()` selects success **200**, unauthorized **403**, invalid **400**, then
exception failure **500**, in that order. Authentication **401** is an HTTP ingress override, not result status precedence. Safe exception messages must already be supplied; result
construction does not log or redact secrets.

These are output envelopes. To decode a remote response, use a consumer DTO rather
than decoding into the private finalized result state.

See [validation findings](../validation/index.md), [query results](../queries/results.md),
[the compiling example](../../../../example_test.go) and [the parity map](../../../parity.md).
