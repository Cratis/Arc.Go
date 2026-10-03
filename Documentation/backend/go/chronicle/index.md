---
title: Chronicle integration
description: Return events from Go commands, inject keyed read models, and run aggregate decisions in one command-owned transaction.
---

An Arc command can return a registered event instead of calling the event store. The optional `github.com/cratis/arc.go/integrations/chronicle` module stages those events and commits them only after the command and its nested commands succeed. Arc's root module still works without Chronicle.

**Source preview:** the integration has no tagged release. It requires Go 1.26 or later and independently fetchable Arc/Chronicle dependencies. The kernel contract tests target `cratis/chronicle:19.29.4-development`. Do not treat this slice as complete C# integration parity; the [parity ledger](../../../parity.md) records the boundaries.

## Run the task-board example

From a repository checkout, start a development kernel and run the example in the nested module:

```bash
docker run -d --rm --name arc-go-chronicle \
  -p 127.0.0.1:35000:35000 cratis/chronicle:19.29.4-development
cd integrations/chronicle
export GOWORK=off GOTOOLCHAIN=local
export CHRONICLE_INTEGRATION_CONNECTION_STRING=chronicle://localhost:35000
go run ./examples/taskboard
```

The example uses development credentials and certificate handling. Replace both before deployment. If the host port is occupied, choose a free host port and change the connection string; do not stop someone else's kernel.

In another terminal, create a task:

```bash
curl -s http://localhost:8080/tasks/create \
  -H 'Content-Type: application/json' -d '{"title":"Write the chapter"}'
```

The successful command envelope contains a generated source ID in `response`. Read its passive projection with `GET /tasks/by-id?id=<response-id>`. The model is registered independently with Chronicle's projection and Arc's query surface. The example maps the selected tenant to the same namespace for both writes and reads; namespace selection alone grants no tenant membership.

Stop the example with Ctrl-C. Stop your development container with `docker stop arc-go-chronicle` when finished.

## One model, two frameworks

You do not need a projection DTO and a separate query DTO. The
[shared-model example](https://github.com/Cratis/Arc.Go/blob/develop/integrations/chronicle/examples/sharedmodel/model.go)
uses one `Inventory` struct with explicit JSON names, an unnamed `ByID` namespace
method and a Fundamentals `ProductName` concept. Its declaration includes:

```go
type Inventory struct {
    ID          string      `json:"id" arc:"identity" chronicle:"key" example:"shared"`
    ProductName ProductName `json:"product_name" chronicle:"set(@registered,from=displayName)"`
    URLValue    string      `json:"URL_value"`
    Note        *string     `json:"note" chronicle:"set(@registered);clear(@cleared)"`
    State       string      `json:"state" chronicle:"value(@registered,value=\"available\")"`
    Transient   string      `json:"-"`
}
```

This is a declaration excerpt; `ProductName`, events and registration helpers live
in the example. `RegisterChronicle` registers events, the read model and a
model-bound projection, binding the two event aliases explicitly. `RegisterArc`
separately registers the same type and adapts `Inventory{}.ByID` through
`queries.Function` on an Arc builder. The query takes a typed reader as a normal
collaborator; composition selects the store and tenant namespace. There is no
shared registry, `init` registration or framework state on the model.

Chronicle maps `displayName` to `product_name`, automatically maps `URL_value`,
and clears the nullable note after `NoteCleared`. Arc returns those exact names;
a nil note is absent from the payload. Arc identity and Chronicle key metadata
remain independent. Follow all seven [shared read-model rules](../queries/model-bound/index.md#shared-arc-and-chronicle-rules).

Run the offline registration/query/HTTP tests with
`go test ./examples/sharedmodel` from the integration module. With the development
kernel configured as above, run the materialization test:

```bash
go test -tags=integration -count=1 -timeout=2m \
  -run TestOneModelProjectsAndServesArcNamespaceQuery ./internal/integration
```

It appends events, waits for the active projection, then checks Arc's direct query
and HTTP payload in two namespaces using the same key. Successful append alone
is not proof that an eventually consistent projection is ready.

## Return events and semantic identities

This excerpt is the command from the [task-board example](https://github.com/Cratis/Arc.Go/blob/develop/integrations/chronicle/examples/taskboard/main.go); a source comparison keeps it in sync:

```go
func (c CreateTask) Handle(context.Context) (commands.Outcome[integration.EventSourceID], error) {
    id, err := integration.NewEventSourceID()
    if err != nil {
        return commands.Outcome[integration.EventSourceID]{}, err
    }
    return commands.Respond(id, TaskCreated(c)), nil
}
```

The selected semantic `EventSourceID` response retargets bare returned events and remains the client response. A raw UUID response does **not** retarget events. `EventForSource` retains its own destination. Retargeting never reloads a previously injected model or aggregate.

Source selection is deterministic: `EventSourceIDProvider`, configured selector, first semantic source-ID or `arc:"key"` field, then a generated ID if no declaration exists. A declared nil or unconvertible key is Unspecified, not permission to generate another ID. Ordinary `ID` field names have no command-key meaning. The SDK adapter also recognizes Chronicle's exact `events.SourceID`.

Use `Events(...)` for explicit event collections and `EventsWithScopes(...)` for ordered entries plus checks. Ordinary slices are not guessed to be events. Typed nil registered events, unregistered wrapped events, invalid metadata and incompatible scopes fail rather than disappearing. A completely empty batch has no append work; a scope-only batch executes its checks. Input values must not be mutated during staging; the SDK snapshots them before returning.

`ConfigureCommand[C]` supplies route, sequence, source selector, compliance subject and concurrency flags. Configure it before Install; you may register commands after Install, but Build rejects any configured `C` that is not exactly registered. `T` and `*T` are distinct command types. `Subject` is a compliance identity, never a principal. Explicit Go wrapper options can supply per-entry route, subject and causation in addition to the C#-style target, tags and occurrence.

## Understand commitment and recovery

Only `invocation.Pipeline()` joins a command transaction. An independently invoked root pipeline gets a separate owner even inside another command. There is no ambient transaction lookup or second integration-owned event buffer.

Ordinary completion runs before the terminal commit. Failed input, handlers, nested commands or ordinary completion roll back open staged work. Store constraints become `constraintViolation` findings; concurrency conflicts become `concurrencyViolation` findings with source, expected position and actual position. Explicit JSON member names are preserved. Store rejection is not weakened by input severity allowances.

Inspect `Result.Completion()` and `commands.CompletionError` on the server. A failed command may have committed earlier work. Unknown outcomes fail the envelope and remove its response but do not prove that persistence failed. There is no automatic commit, handler, aggregate or reactor-command retry; reconcile or use application idempotency before resubmitting.

Immediate SDK writes remain immediate. After authorization, the adapter establishes readiness and subscribes to the selected cached sequence. For an attributable append, an ignored rejection fails the command and rolls back deferred work; an earlier confirmed immediate write cannot be undone. The subscription ends before deferred or early aggregate completion. Raw low-level sequence handles, other sequences, and errors before dispatch are outside this observation window: always inspect direct SDK append results yourself.

Append attribution is an interim safeguard, not execution identity. Within one installed integration, a notification is attributed only when exactly one in-flight command owner holds its store, namespace, sequence and exact request correlation. Zero correlation is not a broadcast. Shared-correlation ambiguity attributes to none and logs once at Debug per in-flight key lifetime, without event payloads. Membership remains until owner completion returns, and notifications matching an integration owner-commit window are excluded, so another command's rejected or successful owner commit cannot poison the remaining command.

This suppression can miss a genuine immediate append racing an owner commit or another same-correlation command. It cannot distinguish an unrelated SDK caller or separate integration sharing the correlation; an append started during ambiguity can also notify after the key becomes a singleton. Always check direct append results, use distinct correlations for independent commands, and do not treat observation as a persistence guarantee. [Chronicle.Go execution-identity issue 47](https://github.com/Cratis/Chronicle.Go/issues/47) tracks the SDK seam needed to remove this limitation.

## Inject keyed read models

Register a model with Chronicle, bind its typed declaration with `sdk.BindReadModel`, then install the integration. The frozen selected-store catalog must identify its projection or reducer. An Arc query declaration alone does not grant Chronicle ownership. `sdk.BindExternalReadModel` is an explicit assertion of an external producer.

Inside a handwritten `commands.Invoke` or `commands.Prepare` adapter, use `commands.RequireReadModel[M](ctx, invocation)`, `ReadModelOrNil[M]`, or presence-bearing `ResolveReadModel[M]`. Ordinary domain methods can keep their model parameter. The current generator does not yet emit these integration-specific parameter adapters.

Missing/blank keys fail even for optional injection. A valid key with no model gives nil for optional reads and `dependencyUnavailable` for required reads. A present zero-valued model remains present. Successful reads, including absence, are cached per frame, model, key and coordinates; Provide and Handle reuse them, while child frames do not. Failures are never cached as absence.

The SDK typed reader owns codecs, collection normalization and release. Kernel reads are not decrypted twice; passive reducer reads follow the SDK's local release contract. Snapshot progress is diagnostic, not protected-decision evidence. A materialized projection can lag. Validation filters may read models without starting transaction participants or running Provide/Handle.

## Make aggregate decisions

An aggregate directly embeds `*integration.AggregateRoot`. `DefineAggregate` accepts its constructor and typed `OnAggregateEvent` callbacks. `factory.Get(ctx, invocation)` resolves it by the original command key. Re-resolve for each callback; old root capabilities expire, and aggregate objects must not be used concurrently or retained beyond the command.

History is loaded once per root, aggregate type, source and route. Empty history enrolls `NoMatchingEvent`; otherwise the check uses the last **loaded** position, including a real position zero. No later tail read replaces that expectation. `Apply` stages before folding local state; a failing fold poisons the transaction even if its caller ignores the error. `Failed(message, validation.Error)` blocks automatic commitment. Warnings and information do not independently block persistence.

The default stream type is the aggregate's simple type name. `WithAggregateRoute` overrides routing; `ConfigureCommand.Sequence` selects the shared command sequence. History and dispatch must agree. Unknown generations and missing handlers fail explicitly. History is buffered, not a bounded-memory stream.

Explicit `Commit` checks the root execution's `CheckRecordedFailures` guard before persistence. Already-recorded failures in this frame or an ancestor, including ignored nested authorization, validation and command-lookup failures, prevent commitment. Nested Validate remains advisory during Execute; the guard cannot predict failures that occur after commitment.

Explicit `Commit` finalizes the **whole shared owner**, not only this aggregate's events. Later staging fails without creating a successor. Its positions describe the shared batch and cannot be attributed to one aggregate in a mixed command. Returning `AggregateCommitResult` adopts its diagnostics into the Arc envelope, including warning findings after successful persistence. A later command failure retracts the response but retains the committed completion report; it cannot undo already-persisted events.

## Resolve concurrency for the actual target

With no flags or explicit scopes, the SDK's configured default strategy remains authoritative. Command flags resolve only their selected dimensions through `Sequence.ResolveScope`, separately for each actual target, and cache the result within that frame. Explicit `Resolve` scopes also use the configured policy. Aggregate-loaded scopes take precedence over flag resolution for aggregate mutations.

`UpperBound(n)` rejects a newer matching tail; it is **not equality** and can accept absent or lower history. Use `NoMatchingEvent` for an empty-history requirement. Default route sentinels can mean wildcard selection. Incompatible checks sharing a source label fail before append; arbitrary multi-boundary checks for one source are not synthesized. In particular, two aggregate types with different default stream types, or differing routes on one source, cannot share this guarded owner. Use separate source IDs or the same route and expectation. The pinned SDK requires source-bound scope labels to equal the source ID, so source+route labels cannot work around this restriction.

C# Chronicle 19.29.1 also rejects conflicting source scopes in strict ordered batches. Its legacy aggregate mutation path can overwrite an earlier source scope; Arc.Go deliberately fails closed instead of discarding a loaded-history check.

The SDK's resolved expectation is opaque. The adapter retains it as an immutable `ProviderResolved` token, including resolved unchecked empty tails, rather than guessing a number or reading the tail again. Tokens cannot cross provider or coordinate boundaries.

## Execute commands from reactors

Create `ReactorCommands` with an explicit principal, store and replay policy. Register `sdk.CommandEffects(bridge, reflect.TypeFor[YourCommand]())` through Chronicle's `RegisterReactorSideEffectHandler` before constructing the client. Bind the bridge to the built Arc application before observers start. Reactor methods can then return commands or collections of commands; the SDK preflights and executes those effects before acknowledgement.

The imperative path is `bridge.Execute(ctx, sdk.DeliveryFrom(eventContext, delivery), command)`, called in the reactor handler body, not an After hook. Each command gets a fresh root Arc operation and completion owner. Both Go errors and unsuccessful envelopes fail delivery. Execution stops at the first failed command; earlier commits survive and may repeat on kernel recovery.

`LiveOnly` is the recommended explicit automation policy. SDK `OnceOnly` and replay authoring remain visible; replay exclusion is not deduplication. Use the stable delivery ID as an application idempotency input where necessary. Event `CausedBy` metadata never grants Arc roles. Per-event correlation and causation are preserved rather than cached on a batch scope.

## Wire lifecycle and audit deliberately

Construction and Build perform no network I/O. Start Arc admission, bind reactor command bridges, then call `integration.Start(ctx)` for configured `StartupNamespaces`. Begin HTTP serving only after required readiness succeeds. During shutdown, unregister/join reactors, drain Arc, then close the client. Default clients are borrowed. `OwnClient: true` transfers closure to an explicit, once-only `integration.Close()` call; it is not a late automatic Arc shutdown hook.

Authenticated principal subject and display name map to audit identity. Username requires an explicit trusted actor mapper; anonymous principals become Chronicle's canonical unknown actor. Tenant Default/NotSet maps to namespace `Default`; other tenants preserve their exact value.

Command causes include logical short/full command identity and sequence. Input properties are **not audited by default**. The explicit Audit callback must supply only approved non-PII scalar values; unknown nested objects are never serialized automatically. Reserved names cannot be overwritten. Values are bounded to 1024 characters plus a truncation marker and an 8192-byte raw property-name/value budget. This fail-closed opt-in differs from C#'s reflected-property auditing.

## Verify independently

From `integrations/chronicle`, with a ready development kernel:

```bash
export GOWORK=off GOTOOLCHAIN=local
export CHRONICLE_INTEGRATION_CONNECTION_STRING=chronicle://localhost:35000
export CHRONICLE_INTEGRATION_IMAGE_DIGEST="$(docker inspect arc-go-chronicle --format '{{.Image}}')"
go test -count=1 -timeout=2m ./...
go test -tags=integration -count=1 -timeout=2m ./internal/integration ./examples/taskboard
python3 scripts/check-boundaries.py
```

Integration-tagged tests fail when the endpoint variable is absent. They exercise HTTP commands, atomic rejection/readback, tenants, shared projection/query models, projection injection, aggregate competition, reactor-returned commands, failed observer partitions and ignored immediate-append rejection. Root and tools gates run separately; `./...` does not cross module boundaries.

Protected decisions/enrollment tokens, watches, aggregate snapshots, historical-generation aggregate decoding, full compliance authoring and general operation compensation remain unsupported. None is implied by an ordinary injected model or a successful snapshot test.
