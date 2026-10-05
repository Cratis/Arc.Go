---
title: Validate with go-playground/validator
description: Report go-playground/validator rules as Arc validation results through a validator callback.
---

Arc's portable `validate:"required"` tag covers presence, but your commands
need e-mail formats, lengths and rules on nested items. You already know
[go-playground/validator](https://github.com/go-playground/validator). This
recipe runs it inside an Arc validator, so its failures become ordinary Arc
validation results: the handler never runs, `/validate` reports the same
findings, and the frontend sees members it can attach to form fields.

## Configure the rules

This code is compiled and tested in the [recipes module](index.md):

```go
// NewRules returns a validator that reads the `rules` struct tag and reports
// JSON member names. Arc already owns the `validate` tag, which accepts only
// required and skipConcept and fails registration on anything else.
func NewRules() *validator.Validate {
    rules := validator.New(validator.WithRequiredStructEnabled())
    rules.SetTagName("rules")
    rules.RegisterTagNameFunc(func(field reflect.StructField) string {
        name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
        switch name {
        case "-":
            return ""
        case "":
            return field.Name
        }
        return name
    })
    return rules
}
```

:::caution[Do not put go-playground rules in the validate tag]
go-playground reads `validate` by default, and so does Arc. Arc rejects an
unknown rule such as `validate:"email"` when you register the command. Keep
`validate:"required"` for Arc and put go-playground rules in `rules`.
:::

## Adapt failures to Arc results

```go
// Validator adapts rules to an Arc validator for T. Each failed rule becomes
// an Error finding on its wire member, so Arc rejects the command or query
// before its handler runs, on /validate as well as on execution.
func Validator[T any](rules *validator.Validate) validation.Validator[T] {
    return validation.ValidatorFunc[T](func(ctx context.Context, value T) ([]validation.Result, error) {
        err := rules.StructCtx(ctx, value)
        var failures validator.ValidationErrors
        if !errors.As(err, &failures) {
            return nil, err // nil, or a programming error such as a non-struct T.
        }
        results := make([]validation.Result, 0, len(failures))
        for _, failure := range failures {
            // Namespace is "Type.member.nested[0].field"; drop the Go type.
            _, member, _ := strings.Cut(failure.Namespace(), ".")
            rule := failure.Tag()
            if failure.Param() != "" {
                rule += "=" + failure.Param()
            }
            results = append(results, validation.Result{
                Severity:     validation.Error,
                Message:      fmt.Sprintf("%s does not satisfy %s.", member, rule),
                Members:      []string{member},
                ReasonDetail: &rule,
            })
        }
        return results, nil
    })
}
```

The callback returns an error only for a programming mistake, such as a
non-struct `T`; Arc treats that as a failure of the validator, not as a
finding. Messages are client-visible, so they name only the member and the rule.

## Register the validator

Create the rules once and share them; a `*validator.Validate` is safe for
concurrent use:

```go
rules := structvalidation.NewRules()
err := commands.Register[OpenAccount](builder,
    commands.Handle(func(OpenAccount, context.Context) (string, error) {
        opened.Add(1)
        return "opened", nil
    }),
    commands.WithPath[OpenAccount](accountPath),
    commands.WithValidator(structvalidation.Validator[OpenAccount](rules)),
)
```

Queries take the same validator through `queries.WithValidator`, which checks
arguments bound from GET query strings and QUERY bodies alike.

## What your clients see

For `{"email":"not-an-email","labels":[{"value":"ok"},{"value":"x"}]}`, both the
command and its `/validate` endpoint return 400 with `isValid: false` and two
results: one on member `email` with `reasonDetail` `email`, and one on
`labels[1].value` with `reasonDetail` `min=2`. Every result has reason `rule`.
Arc's own `validate:"required"` findings appear beside them.
