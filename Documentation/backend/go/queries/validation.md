---
title: Query validation
description: Validate successfully bound argument models and block every retained query finding.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Binding answers whether caller input can form an argument model. Validation answers
whether that model is acceptable. Keep required transport presence in `query` tags,
and put business rules in model methods or typed validators.

## Validation sources

The default graph runs a matching `Validate(context.Context) ([]validation.Result,
error)` model method, registered graph/concept validators, and the supported
`validate:"required"` tag pass. Nil `PipelineOptions.Validation` still enables
model methods and portable tags; a supplied graph adds its registrations.

`WithValidator[A]` appends direct typed validators in option order.
`WithScopedValidator[A]` appends lazy factories constructed only in the authorized
validation stage. `WithoutModelValidation[A]` explicitly disables graph/model/tag
validation for this query, not explicit validators or transport requiredness.

See [shared validation](../validation/index.md) for graph traversal, concept owning
members, cycles and the bounded annotation subset. A Go model `Validate` method is
an explicit Go convention, not a claim that C# automatically invokes such a method.

## Every retained finding blocks

Queries have no allowed-severity relaxation. Unknown, information, warning and
error findings all prevent performer dependency construction and data publication.
Recognized `validation.Failure` findings stay safe and inspectable. Unexpected
validator errors or panics produce `validatorFailed` with exactly
`The value could not be validated.` Cancellation remains cancellation, not a rule.

The graph is intentionally bounded: no arbitrary map traversal, reflective getters,
full DataAnnotations emulation or client rule extraction is promised. Use explicit
typed validators for unsupported structures. See [query arguments](model-bound/query-arguments.md)
for missing versus malformed input and [pipeline errors](query-pipeline.md) for
cleanup failures joined with validation errors.
