---
title: Validate commands
description: Combine model Validate methods, required tags, registered concept rules, and explicit validators.
---

Reject bad input before fetching handler dependencies. Keep reusable value invariants with concepts, cross-field rules with the command, and preparation-dependent decisions in Provide or Handle.

## Use the model convention

A command implementing `Validate(context.Context) ([]validation.Result, error)` is validated automatically, even with manual handler registration. The method set is exact: a registered value never synthesizes a pointer receiver. This is a Go-specific convention; it does not claim a C# model Validate method is automatically invoked.

A nil `PipelineOptions.Validation` still enables model methods and supported tags. `WithoutModelValidation[C]` explicitly opts out of graph/model/tag validation for that command; separately attached `WithValidator` callbacks still run.

## Compose registered and explicit rules

Supply a built `validation.Graph` for registered model and concept validators. The graph permits one registry validator per exact type, ordinary or concept. Model methods, registered rules, and required tags have distinct identities; explicit `WithValidator` and `WithScopedValidator` chains remain additive and ordered.

Factories activate only during validation, after authorization. Explicit validator factories may declare DI keys; Build checks the catalog without activating them. Ordinary missing dependencies remain infrastructure failures, not fabricated business findings.

## Know the bounded graph

The graph validates nonnull runtime nodes before descending readable exported fields and collections in declaration/element order. Shared pointer identity stops cycles and duplicate instance validation; equal distinct instances remain distinct. Collection paths retain their owning member without numeric indices.

Concept findings attach to their owning field, dropping concept-internal `value`. `validate:"skipConcept"` suppresses only the immediate child's concept validator. Arbitrary maps are opaque; use an explicit validator. Traversal is bounded to 64 levels and does not invoke reflective getters.

`validate:"required"` is a separate top-level tag pass. It rejects nil and empty/whitespace strings, not supplied numeric zero or false. Rich range, length, email, regex, recursive annotations, and client rule projection are not implemented here.

## Distinguish rejection from infrastructure

`validation.Invoke` validates findings and maps unexpected validator failures to reason `validatorFailed` and exactly `The value could not be validated.` The local error retains diagnostics without publishing them as a second exception. Cancellation remains cancellation.

Input-stage [severity policy](validation-severity-filtering.md) decides which findings survive. A preflight Validate success is advisory, not proof of a concurrency-safe decision or a committed effect.
