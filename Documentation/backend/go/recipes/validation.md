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
// NewRules returns a validator that reads the `playground` struct tag and
// reports Arc wire member names. Arc owns both `validate` (required and
// skipConcept) and `rules` (JSON portable rule descriptors).
func NewRules() *validator.Validate {
    rules := validator.New(validator.WithRequiredStructEnabled())
    rules.SetTagName("playground")
    rules.RegisterTagNameFunc(func(field reflect.StructField) string {
        name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
        switch name {
        case "-":
            return ""
        case "":
            return serialization.CamelCase(field.Name)
        }
        return name
    })
    return rules
}
```

:::caution[Arc owns validate and rules]
go-playground reads `validate` by default, and so does Arc. Arc rejects an
unknown rule such as `validate:"email"` when you register the command. Arc also
owns `rules`, which accepts a JSON array of portable rule descriptors, not
comma-separated go-playground rules. Keep `validate:"required"` and portable
`rules` for Arc; use `playground:"required,email"` or `playground:"max=10"`
for go-playground, including on declarations consumed by arc-gen.
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
            member := wireMember(reflect.TypeOf(value), failure.StructNamespace())
            var members []string
            if member != "" {
                members = []string{member}
            }
            rule := failure.Tag()
            if failure.Param() != "" {
                rule += "=" + failure.Param()
            }
            results = append(results, validation.Result{
                Severity:     validation.Error,
                Message:      fmt.Sprintf("%s does not satisfy %s.", member, rule),
                Members:      members,
                ReasonDetail: &rule,
            })
        }
        return results, nil
    })
}

// wireMember maps the Go field namespace, retaining collection indexes but
// omitting anonymous struct segments that Arc flattens on the wire. Unknown
// or hidden fields (including memberless struct-level errors) target the model.
func wireMember(t reflect.Type, namespace string) string {
    _, path, _ := strings.Cut(namespace, ".")
    var members []string
    for path != "" {
        for t.Kind() == reflect.Pointer {
            t = t.Elem()
        }
        if t.Kind() != reflect.Struct {
            return ""
        }
        end := strings.IndexAny(path, ".[")
        if end < 0 {
            end = len(path)
        }
        field, ok := t.FieldByName(path[:end])
        if !ok {
            return ""
        }
        path, t = path[end:], field.Type
        name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
        base := t
        for base.Kind() == reflect.Pointer {
            base = base.Elem()
        }
        if name == "-" || !field.IsExported() && !field.Anonymous {
            return ""
        }
        flatten := field.Anonymous && name == "" && base.Kind() == reflect.Struct
        if name == "" {
            name = serialization.CamelCase(field.Name)
        }
        for strings.HasPrefix(path, "[") {
            end = strings.IndexByte(path, ']')
            if end < 0 {
                return ""
            }
            name += path[:end+1]
            path = path[end+1:]
            for t.Kind() == reflect.Pointer {
                t = t.Elem()
            }
            switch t.Kind() {
            case reflect.Array, reflect.Slice, reflect.Map:
                t = t.Elem()
            default:
                return ""
            }
        }
        if !flatten {
            members = append(members, name)
        }
        path = strings.TrimPrefix(path, ".")
    }
    return strings.Join(members, ".")
}
```

The callback returns an error only for a programming mistake, such as a
non-struct `T`; Arc treats that as a failure of the validator, not as a
finding. Messages are client-visible, so they name only the member and the rule.
Untagged fields and `json:",omitempty"` use Arc's camelCase names; untagged
anonymous structs are flattened. Memberless struct-level errors have no members,
so the client shows a model-level finding rather than targeting an empty field.

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
