---
title: Run your first Arc.Go application
description: Run the Arc.Go hosting example, post a JSON greeting command to /api/greet over HTTP, and read the Hello, Ada response without a container or event store.
---

You want a command endpoint, not a collection of transport handlers. Arc maps
registered commands and queries when you build the application. The Go API
supports snapshot and observable HTTP. Start with a unary command here;
then use [observable queries](../queries/observable-queries.md) for live results.
Observable generation remains separate tools work.

## Run a greeting command

You need Go 1.26 or later and two terminals. From the repository root, run the
[hosting example](../../../../examples/hosting/main.go) in the first terminal.
Leave it running while you send requests from the second terminal:

```sh
go run ./examples/hosting
```

The business model is an ordinary Go type:

```go
type Greet struct {
    Name string `json:"name" validate:"required"`
}

func (c Greet) Handle(_ context.Context) (string, error) {
    return "Hello, " + c.Name, nil
}
```

This excerpt uses `context` from the standard library. The application registers
`Greet.Handle` with `commands.Register` and `commands.Handle`, then builds and runs
Arc. No separate endpoint-mapping step can be forgotten.

In the second terminal, send a greeting:

```sh
curl -H 'Content-Type: application/json' -d '{"name":"Ada"}' http://127.0.0.1:8080/api/greet
```

The command envelope contains `"response":"Hello, Ada"`. An empty name returns
400. Post the same body to `/api/greet/validate` to validate without invoking the
handler. When you finish, press Ctrl+C in the server terminal to initiate graceful
shutdown.

## Compose your application

You have already run the application; no second startup step is needed. To adapt
the sample for your own application, use its composition function below. This
excerpt assumes the model above and imports `arc`, `commands` and `context`:

```go
func run(ctx context.Context) error {
    builder, err := arc.NewBuilder(arc.Options{})
    if err != nil { return err }
    if err := commands.Register[Greet](builder, commands.Handle(Greet.Handle)); err != nil {
        return err
    }
    app, err := builder.Build()
    if err != nil { return err }
    return app.Run(ctx, "127.0.0.1:8080")
}
```

You own signals and error reporting. `Build` validates composition without
starting a listener or activating dependencies. `Run` starts admission and owns
the server. Continue with [hosting](hosting.md) and [authentication](../authentication/index.md)
before exposing private operations beyond loopback.
