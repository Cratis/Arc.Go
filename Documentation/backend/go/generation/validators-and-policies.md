---
title: Generate validator and policy registrations
description: Declare typed validators and named authorization policies with arc:validator and arc:policy so RegisterArtifacts registers them.
---

A command declares `//arc:authorize policy=Editors`, and the builder refuses to
build until something registers a policy named `Editors`. The validator for that
command is a small type in the same package, yet you still have to remember to
call `validation.Register` in your composition code. C# Arc discovers both by
type. In Go, `arc-gen` registers them for you when you mark the declaration
with `//arc:validator` or `//arc:policy`.

The examples on this page are excerpts from the compiled consumer that the
generator's tests run; `rename`, `code`, `ownerRules`, `auditors` and `auditLog`
are ordinary application types.

The generated code calls the same public registrars you would call by hand,
from the package's `RegisterArtifacts`. Nothing is discovered at run time, and
the generator never constructs a validator or policy while it analyzes code.

## Declare a shared validator

Mark a struct whose zero value is ready to use. Its `Validate` method names the
type it validates:

```go
//arc:validator
type renameValidator struct{}

func (renameValidator) Validate(_ context.Context, command rename) ([]validation.Result, error) {
    if command.Name == "" {
        return []validation.Result{{Severity: validation.Error, Message: "Name required", Members: []string{"name"}}}, nil
    }
    return nil, nil
}
```

`RegisterArtifacts` then contains exactly the call you would write yourself:

```go
validation.Register[rename](builder.Validators(), renameValidator{})
```

The registered instance is shared and must be safe for concurrent use. When
`Validate` has a pointer receiver, the generator registers `&renameValidator{}`
instead. The validator applies wherever the validation graph meets the exact
type, so a validator for a nested struct also runs for every command that
contains it.

Add `concept=true` to register an opaque concept leaf with
`validation.RegisterConcept`. Its findings attach to the member that holds the
concept:

```go
//arc:validator concept=true
type codeValidator struct{}

func (*codeValidator) Validate(_ context.Context, value code) ([]validation.Result, error) {
    if strings.ToUpper(string(value)) != string(value) {
        return []validation.Result{{Severity: validation.Error, Message: "Code must be upper case"}}, nil
    }
    return nil, nil
}
```

## Declare a scoped validator with dependencies

When a validator needs services, mark a package-level constructor instead of
the type. Each validation calls the constructor through
`validation.RegisterScoped`; its parameters are resolved like `Handle`
dependencies:

```go
//arc:validator
func newOwnerValidator(ctx context.Context, rules ownerRules) (*ownerValidator, error) {
    return &ownerValidator{rules: rules}, nil
}
```

A `context.Context` parameter receives the validation context. Every other
parameter becomes a field on `ArcBindings`, here `ResolveOwnerRules`. A nil
callback falls back to `execution.Resolve` and declares the exact dependency key,
so `Build` fails early when the scope factory cannot supply it. The constructor
may return `T` or `(T, error)`. A non-nil error fails that validation, and a nil
instance is rejected by the runtime. `concept=true` selects
`validation.RegisterScopedConcept`.

## Declare a named policy

`//arc:policy name=<name>` registers an `authorization.Policy` under the name
that `//arc:authorize policy=<name>` declarations refer to. A struct registers
its zero value as a shared policy through `Policies().Register`:

```go
//arc:policy name=Editors
type editors struct{}

func (editors) Authorize(_ context.Context, value authorization.Context) (authorization.Decision, error) {
    if value.Principal.HasRole("Editor") {
        return authorization.Allow(), nil
    }
    return authorization.Deny("editors only"), nil
}
```

A constructor registers a scoped policy through `authorization.RegisterPolicy`,
with the same dependency rules as a scoped validator:

```go
//arc:policy name=Auditors evaluates-anonymous=true
func newAuditors(log auditLog) auditors { return auditors{log: log} }
```

`evaluates-anonymous=true` sets `PolicyOptions.EvaluatesAnonymous`, so guests
reach the policy instead of being denied before it runs. It defaults to `false`.

## Contract and diagnostics

| Directive | Declaration | Generated registration |
| --- | --- | --- |
| `//arc:validator` | Struct type | `validation.Register[T]` with the zero value |
| `//arc:validator concept=true` | Struct type | `validation.RegisterConcept[T]` |
| `//arc:validator` | Constructor function | `validation.RegisterScoped[T]` with a factory and dependency keys |
| `//arc:validator concept=true` | Constructor function | `validation.RegisterScopedConcept[T]` |
| `//arc:policy name=N` | Struct type | `Policies().Register("N", value, options)` |
| `//arc:policy name=N` | Constructor function | `authorization.RegisterPolicy("N", factory, options, keys...)` |

The generator rejects these declarations with a file, line and column diagnostic:

- A type or constructor result whose method set lacks the exact
  `Validate(context.Context, T) ([]validation.Result, error)` or
  `Authorize(context.Context, authorization.Context) (authorization.Decision, error)`
  method. Lookalike `context`, `validation` or `authorization` types from another
  package never match, and the model-bound `Validate(context.Context)` method is
  not a typed validator.
- Generic, aliased or non-struct types, methods, and variadic or generic constructors.
- Constructors that return anything other than `T` or `(T, error)`.
- Validators for interfaces, functions, channels or `unsafe.Pointer`, and for
  types that the declaring package cannot name.
- Two validators for the same type, or two policies with the same name, in one package.
- `arc:policy` without `name=`, option values other than `true` or `false`, and
  combinations with `arc:command`, `arc:readmodel`, `arc:authorize`,
  `arc:allow-anonymous`, `arc:exclude-from-discovery` or `arc:ignore`.

Duplicates across packages are reported by the runtime registries as
`validation.ErrDuplicate` or `authorization.ErrDuplicate` when
`RegisterArtifacts` runs.

## Limits

- Validators and policies are registered by `RegisterArtifacts`, so a package
  that declares them also needs an `arc:command`, `arc:readmodel` or derived
  model. A package with only validators or policies is rejected rather than
  silently generating nothing; register those manually for now.
- The generator does not check that every `//arc:authorize policy=` name has a
  registered policy. The builder checks that at `Build`.
- C# Arc discovers validators by base type and resolves them from the service
  provider. Go has no assembly scanning, so the directive is the opt-in, and
  constructor parameters replace constructor injection.

See [directives and signatures](reference.md) for the other directives and
[generating model-bound adapters](index.md) for the generation workflow.
