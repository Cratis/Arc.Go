# Plain Go wiring without a container

From the repository root, run:

```sh
go run ./examples/nocontainer
```

Expected output:

```text
authorized: true
validation findings: 0
hello, Arc
resources closed: true
```

The example uses role authorization, model-tag validation, and an execution scope
with explicitly constructed resources. Authorization denial and validation
failure skip work but still dispose resources. This is foundation composition,
not an implemented command/query pipeline or HTTP server.

No source file in this example imports a Fundamentals `dependencyinjection`
package. Arc's small DI contracts may occur transitively through `execution`, but
no runtime package requires the default container. Check that boundary with
`python3 scripts/check-no-container.py`; run the behavior and output tests with
`go test ./examples/nocontainer`.
