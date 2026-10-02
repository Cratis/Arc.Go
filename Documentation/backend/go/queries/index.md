---
title: Queries in Go
description: Keep query behavior beside its read model and execute typed snapshots without a container.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Keep selection logic beside the state it returns. Arc.Go queries belong to an
ordinary read-model struct, with unnamed value-receiver methods standing in for
C# static methods. You can also use explicitly owned top-level functions.

This surface executes **snapshots**. HTTP hosting, generated adapters, subscriptions
and persistence adapters are separate work; source directives do not register
queries at runtime. No database or dependency-injection container is required.

## Return a first snapshot

Save this complete program in a module that depends on Arc.Go, then run `go run .`.
It prints `Ada`.

```go
package main

import (
    "context"
    "fmt"

    "github.com/cratis/arc.go/metadata"
    "github.com/cratis/arc.go/queries"
)

//arc:readmodel
type Author struct {
    Name string `json:"name"`
}

func (Author) All(context.Context, queries.NoArguments) ([]Author, error) {
    return []Author{{Name: "Ada"}}, nil
}

func run() error {
    var registry queries.Registry
    err := queries.Register[Author](&registry, "All",
        queries.Function(func(ctx context.Context, args queries.NoArguments) ([]Author, error) {
            return Author{}.All(ctx, args)
        }),
        queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true}),
    )
    if err != nil {
        return err
    }
    pipeline, err := registry.Build(queries.PipelineOptions{})
    if err != nil {
        return err
    }
    result, err := queries.Perform[[]Author](context.Background(), pipeline, "Author.All", queries.Request{})
    if err != nil {
        return err
    }
    if !result.IsSuccess() {
        return fmt.Errorf("query failed with status %d", result.StatusCode())
    }
    authors, _ := result.Data()
    fmt.Println(authors[0].Name)
    return nil
}

func main() {
    if err := run(); err != nil {
        panic(err)
    }
}
```

`Register` records the owning model, query identity and typed callback. `Build`
validates the catalog without invoking the callback. `Perform` opens an operation
scope, checks security and validation, and returns a ready result. A successful nil
model is ready-null, not a pending query.

## Choose the next step

- [Model-bound authoring](model-bound/index.md): namespace methods, directed functions and shared read models.
- [Query arguments](model-bound/query-arguments.md): explicit requiredness, defaults and presence.
- [Paging](model-bound/paging.md): provider rendering versus already-windowed pages.
- [Query pipeline](query-pipeline.md): lifecycle, staging and local errors.
- [Query results](results.md): the unchanged wire envelope and status precedence.
