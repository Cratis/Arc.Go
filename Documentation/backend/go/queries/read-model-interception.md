---
title: Read-model interception
description: Transform rendered models in deterministic order without treating masking as row authorization.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Use interception when a returned model needs a safe presentation transformation,
such as removing a private field. Render first, then transform each returned item.
Do not use masking as row authorization: a renderer may already have counted rows.

## Register an exact transformation

`RegisterReadModelInterceptor[T](registry, name, factory)` appends a named factory.
It supplies a `ReadModelInterceptor[T]`; `InterceptorFunc[T]` adapts an ordinary
`func(context.Context, T) (T, error)`. The executed masking example is
`ExampleRegisterReadModelInterceptor` in `queries/example_test.go`.

Pointer and value registrations are exact and independent. An interceptor for
`Item` does not automatically intercept `*Item`. There is no runtime closure of
open generic interceptors. Duplicate names for the same exact type fail.

## Order and ownership

Applicable factories activate only after rendering. Each item flows through
interceptors in registration order, and items run sequentially in collection order.
This deliberately differs from C#'s concurrent item processing while preserving
result order. Nil single models and nil pointer elements skip item transformation.

Collection membership is copied before transformation, not deeply cloned. Return
safe transformed models; a pointer interceptor that mutates a shared model can
still expose a race or modify repository state. Shared callbacks must be concurrent-safe
across independent query executions.

A factory error, panic, cancellation or item failure retracts the whole snapshot.
No partly masked collection is published. Interceptors cannot secure pre-render
counts; apply row authorization at the selection source.
