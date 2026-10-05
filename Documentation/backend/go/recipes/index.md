---
title: Recipes for common Go hosts and libraries
description: Compiled, tested recipes for mounting Arc in Chi, Gin and Echo and adding JWT, CORS/CSRF, OpenTelemetry and go-playground validation.
---

Arc is a plain `http.Handler`, so it fits into the router, authentication,
tracing and validation libraries your service already uses. The glue is
small, but each library has one detail that silently breaks Arc's contract:
a router that swallows the QUERY method, a fallback that appends a body to
Arc's empty 404, or a struct tag both libraries claim. These recipes show the
glue that works and the tests that prove it.

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
and request cancellation, plus each recipe's own behavior. A documentation test
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
application; do not import `github.com/cratis/arc.go/recipes`. The recipes do
not cover databases, message brokers or other optional integrations.
