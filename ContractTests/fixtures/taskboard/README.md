# Task-board HTTP conformance

This Go-owned gate adapts the nine selected task-board assertions from
[Arc.Kotlin's contract.py](https://github.com/Cratis/Arc.Kotlin/blob/23c93a3f70008f20d99c28868010b08e3123ea97/ContractTests/HttpConformance/contract.py).
The reference fixture targets **published C# Arc 22.14.0**, not current C# source.
The original and this adaptation are Copyright (c) Cratis, MIT licensed; see
[LICENSE](https://github.com/Cratis/Arc.Go/blob/main/LICENSE).

## Pinned inputs

| Input | Pin |
| --- | --- |
| Arc.Kotlin repository revision | `23c93a3f70008f20d99c28868010b08e3123ea97` |
| Original `contract.py` SHA-256 | `7413e4eea0fa6d287adfc221d7a3fc8a7a9b64c81f91506ea26f19acd752201d` |
| Published C# fixture dependency | Arc `22.14.0` |
| Go assertions | `ContractTests/taskboard_contract_test.go` |
| Real Go host | `ContractTests/internal/taskboard/host` |

The original source was read at the pinned clean revision. CI needs neither a
sibling clone nor Python/.NET: the reviewed Go assertions are committed here.
These tests run **against Go only**; they do not launch the pinned .NET, Kotlin,
or Java fixtures and do not claim a fresh cross-runtime comparison.

The separate `arctest` scenario design follows C# Arc
`7c1e78075b737df64f69fddfaae83374f75e3612`, specifically
`Source/DotNET/Testing/Commands/CommandScenario.cs`,
`Commands/CommandResultShouldExtensions.cs`, and `Queries/QueryScenario.cs`.
That newer scenario reference does not change the historical HTTP profile.

## Required cases

The gate requires all nine cases for each selected host. A `-run` filter that
selects a host but excludes any contract cases fails rather than passing vacuously.

1. The initial GET list is an empty array.
2. Two creates return distinct canonical UUIDs and typed `{id, title}` responses.
3. GET by ID selects each requested task.
4. Structured QUERY by ID selects each task and sends `Cache-Control: no-store`.
5. Enumerable QUERY returns the complete task list.
6. Successful validate omits the response and does not mutate the list.
7. Completion returns the updated task and changes only that task in the list.
8. Malformed command JSON returns 400, `malformedRequest`, no response or parser
   exception/stack/detail, and no mutation.
9. An unknown route returns 404.

Go additionally checks JSON content type, exact header/body correlation against
a supplied canonical UUID, required success fields, ready snapshots, and default
paging. The malformed envelope is asserted completely, including its fixed safe
message. Success payloads are compared completely; unrelated extra success
envelope fields are not normalized away or treated as cross-runtime equality.
Only task-list ordering and JSON object-key order are ignored. IDs are never
replaced with placeholders: relationships are asserted first and throughout.

## Run the gate

From the repository root:

```sh
GOWORK=off GOTOOLCHAIN=local go test -count=1 -timeout=2m ./ContractTests -run '^TestTaskBoardHTTPConformance$'
```

Both `httptest` and `process` subtests must pass. Normal `go test ./...` also
runs the gate, its anti-vacuity check, and the in-process scenario workflow.
No integration opt-in or external service is needed.

The process lane relaunches the compiled Go test executable with an isolated
helper entry point calling **the same `taskboard.Run` as the command host**. This
avoids nested `go build` invocations and preserves race instrumentation in the
child. It is a separate process using real Arc `Application.Serve`, not an
`httptest` handler pretending to be a host.

To launch the standalone host manually:

```sh
go run ./ContractTests/internal/taskboard/host
```

It binds `127.0.0.1:0` and prints exactly one readiness line, with the allocated
port substituted:

```json
{"kind":"arc-go-conformance-ready","baseUrl":"http://127.0.0.1:PORT"}
```

The command owns OS signal handling. The test helper instead cancels on stdin
EOF, which works on Windows too. Readiness is published only on the first accept,
after application startup. Publication failure fails the host.

## Bounds and evidence

- Child lifetime: 60 seconds; native child test timeout: 55 seconds.
- Startup: 10 seconds, waiting on the readiness line without polling.
- Requests: 3 seconds each; responses: 1,000,000 bytes maximum.
- Combined child output: 64 KiB maximum.
- Cleanup: 5 seconds for graceful EOF shutdown, then kill and a 3-second join
  deadline. Forced termination or a failed join fails the test.
- HTTP proxy use and redirects are disabled. Origins must be explicit IPv4
  loopback HTTP addresses with a port and no credentials/path/query/fragment.

Raw requests, status, headers and response text are retained in Go's test log;
use `-v` to display them on success. Failures display the exchanges automatically.
The fixed input hashes above identify the reference independently of generated
UUIDs. There is no automatic golden-update path.

This gate does not cover authentication, tenant isolation, observable transports,
proxy generation, Chronicle, MongoDB, or the richer Arc 22.45.0 HTTP profile.
Those require their own suites; nine passing task-board cases are not whole-product
parity evidence.
