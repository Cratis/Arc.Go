---
title: Commands in Go
description: Put command input and behavior together and execute it through Arc's authorized pipeline.
---

You should not need a second handler type just to act on a command. In Arc.Go, the command owns its `Handle` method. Typed adapters connect that method to authorization, validation, preparation, returned-value handling, and completion without requiring a container.

## Start with a model-bound command

Begin with [model-bound authoring](model-bound/index.md), then [execute the command](command-pipeline.md) from your backend. Result-bearing methods use a method expression; a dependency-free `Handle(context.Context) error` supports type-only registration. Your business methods remain directly callable in unit tests.

There is no runtime source discovery or reflective invocation. Source directives and arbitrary-signature generated adapters are a separate tooling slice; writing a directive alone does not register a command today.

## Add behavior where it belongs

- [Authorization](model-bound/authorization.md) protects the declaration before ordinary dependencies are constructed.
- [Validation](command-validation.md) rejects input, including model-bound `Validate` methods and registered concept rules.
- [Severity filtering](validation-severity-filtering.md) controls which input-stage findings block, without weakening a command floor.
- [Context and keys](command-context.md) supplies invocation metadata without an ambient dependency resolver.
- [Filters](command-filters.md) contributes ordered verdicts.
- [Response value handlers](response-value-handlers.md) consumes returned effect values; [response examples](response-examples.md) explains the explicit return grammar.
- [Protected decision reads](decision-reads.md) guards read-model decisions against competing writes through a provider.
- [Operations](operations.md) declares immediate inline work with commit-aware, best-effort compensation.
- [Execution scopes](command-execution-scopes.md) participates in root completion before resources are disposed.

## Know the boundary

This is a backend command library, not an HTTP host. It preserves the command result envelope, but does not create endpoints or proxies. Chronicle operation participation and durable delivery remain separate integration slices; the manual flat operation core does not establish provider compatibility. Returning an event-shaped struct does not append it automatically. A failed result is not proof that an external write did not commit.
