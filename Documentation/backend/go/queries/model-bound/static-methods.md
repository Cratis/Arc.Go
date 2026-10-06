---
title: Namespace methods and query functions
description: Translate C# static read-model queries into direct, typed Go calls.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Go has no static methods. Use an unnamed value receiver as a namespace: it places
query behavior beside the model without pretending that the model is a handler
instance or a dependency-injection service.

## Declare a namespace method

The declaration below is the model portion of the [first snapshot example](../index.md).

```go
//arc:readmodel
type Author struct {
    Name string `json:"name"`
}

func (Author) All(context.Context, queries.NoArguments) ([]Author, error) {
    return []Author{{Name: "Ada"}}, nil
}
```

The receiver has no name and the method does not depend on instance state. Manual
composition adapts the direct call `Author{}.All(ctx, args)` with `queries.Function`.
No dummy model instance is needed at an application composition site.

Future generated discovery uses only declared, nongeneric methods with unnamed or
blank value receivers and supported owning-model returns. Promoted methods do not
become queries. Explicit pointer/stateful query declarations must be diagnosed,
not invoked reflectively. Generation is separate tooling work; these runtime APIs
do not discover methods or read source comments.

## Declare an owned function

With `Author` defined above, this alternative declaration is equally supported:

```go
//arc:query model=Author name=Recent
func RecentAuthors(context.Context, queries.NoArguments) ([]Author, error) {
    return []Author{{Name: "Grace"}}, nil
}
```

Manual composition uses `queries.Register[Author](registry, "Recent",
queries.Function(RecentAuthors))`, with any desired metadata options. The directive
records intended ownership for generation; it does not register anything by itself.

A method and function cannot both register the same model/query identity.
`ErrDuplicate` reports the conflict. The full name is the logical model identity
plus the query name, independent of the route. Package-private functions can be
called by handwritten adapters in their own package without reflection.

See [dependencies](dependency-injection.md) to add repository parameters without
putting a service locator in business code.
