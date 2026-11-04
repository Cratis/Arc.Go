---
title: Validate with go-playground/validator
description: Report go-playground/validator rules as Arc validation results through a validator callback.
---

Arc's portable `validate:"required"` tag covers presence. Use
[go-playground/validator](https://github.com/go-playground/validator) for
email formats, lengths and nested-item rules, then adapt its failures to Arc
validation results. Invalid input is rejected before the handler runs;
`/validate` reports the same findings, with members a frontend can attach to
form fields.

The registration fragment uses an application-owned `structvalidation`
package containing the copied helpers and their imports. It is not an import
from the unpublished recipes module.

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

Create and configure the rules during application setup, before serving
requests. Reuse the configured instance; a `*validator.Validate` is safe for
concurrent use.

:::caution[Arc owns validate and rules]
go-playground reads `validate` by default, and so does Arc. Arc rejects an
unknown rule such as `validate:"email"` when you register the command. Arc also
owns `rules`, which accepts a JSON array of portable rule descriptors, not
comma-separated go-playground rules. Keep `validate:"required"` and portable
`rules` for Arc; use `playground:"required,email"` or `playground:"max=10"`
for go-playground, including on declarations consumed by arc-gen.
:::

## Adapt failures to Arc results

The adapter validates the bound command or query value with the configured
rules, then maps each rule failure to an Arc finding. Keep `wireMember` with
the adapter in the same application-owned helper package so the findings use
wire names rather than Go field namespaces.

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

This fragment assumes an application `builder`, an `OpenAccount` model and
its `accountPath`. The example's `opened` counter tracks handler calls; your
application supplies its own model, route and handler state. The example
command requires a name and has playground rules for its email and nested
label values. Attach the callback using the configured rules:

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
command and its `/validate` endpoint return 400 with `isValid: false`. The two
playground findings are one on member `email` with `reasonDetail` `email`,
and one on `labels[1].value` with `reasonDetail` `min=2`. Each playground
finding has reason `rule`.

The command also requires a name. Because this request omits it, Arc adds its
own `validate:"required"` finding alongside the two playground findings.
