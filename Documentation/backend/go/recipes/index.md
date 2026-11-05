---
title: Recipes for common Go hosts and libraries
description: Compiled, tested recipes for mounting Arc in Chi, Gin and Echo and adding JWT, CORS/CSRF, OpenTelemetry and go-playground validation.
---

Arc exposes a plain `http.Handler` that you can connect to your service's
router, authentication, tracing and validation libraries. The host must
preserve Arc's method handling and response boundaries, including QUERY and
an empty 404 response.

Choose a host recipe and the optional integrations you need. In your
application, register commands, queries and any validation callbacks before
building Arc. Prepare host-owned resources, start Arc, then serve its handler
through the chosen HTTP host. At shutdown, initiate HTTP draining and Arc
shutdown together, wait for their outcomes, then close resources such as the
tracing provider once the work using them has joined.

## Recipes

| You want to | Recipe |
| --- | --- |
| Mount Arc in a Chi router | [Chi](chi.md) |
| Forward unmatched Gin requests to Arc | [Gin](gin.md) |
| Delegate Echo v5 requests to Arc | [Echo](echo.md) |
| Authenticate bearer tokens with golang-jwt v5 | [JWT bearer tokens](jwt.md) |
| Serve a frontend on another origin safely | [CORS and CSRF](cors-csrf.md) |
| Join callers' traces with otelhttp | [OpenTelemetry](opentelemetry.md) |
| Validate with go-playground/validator | [Validation](validation.md) |

## How the recipes are verified

Every Go block on these pages is an exact excerpt of code in the repository's
[`recipes` module](https://github.com/Cratis/Arc.Go/tree/develop/recipes). Its
tests serve a small Arc application through each recipe over real HTTP and
check commands, `/validate`, GET and QUERY binding, HEAD, Arc's empty 404/405
and request cancellation, correlation/privacy headers, direct SSE/WebSocket
results, disconnect cleanup and application shutdown, plus each recipe's own
behavior. A documentation test
fails when a Go block here no longer matches the tested code.

Run them from the repository root with the module's own manifest:

```bash
cd recipes
GOWORK=off go test ./...
```

The tests cover the library versions pinned in `recipes/go.mod`. Other versions
may behave differently; run the same tests against yours before you rely on a
recipe.

## What the module is not

The `recipes` module is never tagged or published, and Arc's runtime module
does not depend on any of these libraries. Copy the code you need into your
application; do not import `github.com/cratis/arc.go/recipes`. Put copied
helpers, their imports and any local declarations in application-owned
packages. A helper package prefix in a fragment refers to that copied code,
not an import from the unpublished module.

The recipes do not cover databases, message brokers or other optional
integrations.
