---
title: Supply truthful discovery schemas
description: Understand bounded metadata-only schemas and override opaque application codecs.
---

A schema which hides unsupported fields is worse than a startup error. Arc builds
schemas from registration types without constructing providers or running codecs.
Exposed unsupported shapes fail Build with `ErrSchemaUnavailable` and an artifact/type
name; excluded or unavailable catalogs do not force unrelated schemas into your app.

## Supported shapes

Schemas reuse Arc field visibility and wire names. Scalars, pointers, Optional,
ordinary structs, arrays/slices and string-key maps are supported. Approved UUID,
calendar, duration and timestamp representations retain their wire forms.
Numeric enums remain numeric; there is no enum-member registry. Query requiredness
comes from `Registration.Parameters`, including defaults, not dependency types.
Identity schemas describe the selected details type; default empty details is `{}`.

Pointers/Optionals describe nullability. Omission and input optionality are separate
from an output's null representation. Byte slices describe base64. Arbitrary
interfaces, recursive or excessive-depth graphs and opaque custom JSON/text codecs
require a schema override. `json.RawMessage` is an intentionally unconstrained field.

## Override a custom codec

Use `arc.RegisterSchema[T](builder, document)` with a JSON schema object or boolean.
The document is copied and structural keywords are validated before use. No custom
codec is executed to guess its schema. Duplicate exact-type overrides fail.

This is bounded Go schema support, not full .NET JsonSchemaExporter equivalence,
polymorphism, validation-rule export, OpenAPI or TypeScript proxy generation.
