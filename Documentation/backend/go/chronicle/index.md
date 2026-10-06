---
title: Chronicle integration
description: Return events from Go commands, inject keyed read models, and run aggregate decisions in one command-owned transaction.
---

An Arc command can return a registered event instead of calling the event store. The optional `github.com/cratis/arc.go/integrations/chronicle` module stages those events and commits them only after the command and its nested commands succeed. Arc's root module still works without Chronicle.

**Source preview:** the integration has no tagged release. It requires Go 1.26 or later and independently fetchable Arc/Chronicle dependencies. The kernel contract tests target `cratis/chronicle:19.29.4-development`. Do not treat this slice as complete C# integration parity; the [parity ledger](../../../parity.md) records the boundaries.

Arc's [observable sources](../queries/observable-queries.md) can consume an
application-owned feed, but this module does not provide Chronicle read-model
watches. Do not simulate authoritative watches from command append notifications;
a future SDK watch adapter must own cancellation/join and store/namespace/key
selection. MongoDB watches are also separate work.

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

Configure append attribution once when constructing the shared Chronicle client:

```go
client, err := chronicle.NewClient(
    chronicle.WithRegistry(registry),
    chronicle.WithAppendOriginResolver(sdk.ResolveAppendOrigin),
)
```

This is a construction excerpt; configure your connection and authentication too. `sdk.New` borrows the already-built client and cannot retrofit its frozen resolver option. Without this option, arbitrary handler appends are **not attributed**. The task-board example includes it.

Each root execution allocates a fresh, immutable local origin, even without staged work. After mandatory authorization, every executing frame publishes it in Arc's command snapshot. Joined children and grandchildren share the root origin; an independent root Execute selects a new origin even when its context inherits the same correlation or an explicit SDK origin. Advisory Validate publishes no origin: its snapshot resolves to zero and masks inherited attribution.

Notifications must match the exact nonzero origin and the selected store, namespace and sequence. Correlation is diagnostic only, so concurrent same-correlation commands remain separate. SDK-owned unit commits use their own origin and bypass the resolver; they are not counted as immediate handler appends. Resolver errors or panics fail before dispatch without fallback. Origins are local attribution metadata, not authorization credentials or persistence guarantees.

Coverage requires the supplied callback context and this configured client. Appends that discard that context, use another client or raw low-level handles, target other coordinates, or outlive the command's subscription are outside the mechanism. Direct SDK appends by application filters or validators in operation commands before terminal `Begin` are also unattributed and outside the immediate-append mechanism. Zero-origin notifications are ignored. Pre-dispatch failures do not emit notifications. Always inspect every direct append result, including rejected and uncertain outcomes; immediate persistence cannot be rolled back by a later command failure.

## Compensate operations against Chronicle outcomes

A booking command often has to do two things: record `SeatBooked` in Chronicle and reserve the seat in an external system. If the reservation succeeds and the Chronicle commit is then rejected, you need to release the seat. If the events did persist, releasing it would be wrong. Arc's [command operations](../commands/operations.md) handle this split. Chronicle reports what actually persisted, and Arc decides whether compensation is safe.

The installed integration fills Arc's sole operation-classified terminal slot, so operation commands run against Chronicle with no further registration:

```go
type reserveSeat struct{ BookingID, SeatID string }

func (reserveSeat) CommandOperation() {}
func (o reserveSeat) Execute(ctx context.Context, seats SeatService) error {
    return seats.Reserve(ctx, o.BookingID, o.SeatID)
}
func (o reserveSeat) Compensate(ctx context.Context, seats SeatService, _ commands.OperationFailure) error {
    return seats.Release(ctx, o.BookingID)
}

err := commands.RegisterOperation[reserveSeat, SeatService](builder.Commands(),
    func(context.Context, *execution.Scope) (SeatService, error) { return seats, nil })
err = commands.Register[BookSeat](builder, commands.Handle(func(c BookSeat, _ context.Context) (commands.Outcome[commands.NoResponse], error) {
    return commands.Effects[commands.NoResponse](SeatBooked{SeatID: c.SeatID}, reserveSeat{c.BookingID, c.SeatID}), nil
}), commands.WithOperations[BookSeat]())
```

This excerpt is checked against the compiled `integrations/chronicle/operation_example_test.go` example: install the integration first, check each error, and declare `SeatService`, `BookSeat` and `SeatBooked` yourself. Handle only declares work. Arc stages the returned events, enters the operations in order, then runs the terminal Chronicle commit. Recovery runs only after that commit, in reverse order.

The completion report decides recovery:

| Completion | Typical cause | Recovery |
| --- | --- | --- |
| `NoPersistedWork` | An operation failed and nothing was staged or appended | Compensators run |
| `NotCommitted` | The deferred commit was rejected, for example by a unique constraint | Compensators run |
| `Committed` | An operation's attributed immediate append was confirmed before it failed | `RecoverySuppressed`; nothing is reversed |
| `OutcomeUnknown` | An immediate append or the commit lost its acknowledgement | `RecoveryIndeterminate`; reconcile first |
| `MixedCommit` | Confirmed immediate writes plus rolled-back deferred work | `RecoveryIndeterminate`; reconcile first |

Inspect `result.Recovery()` and `result.OperationOutcomes()` on the server. They are backend-only and never reach the HTTP envelope.

Operation commands begin the Chronicle transaction after authorization, filters and validation. The integration records the authorized observation request in its filter. The terminal `Begin` then opens the transaction, publishes the append origin and subscribes, all before `Provide`, `Handle` or any operation runs. The integration itself never persists during filters or validation, and a validation failure leaves no subscription behind. Application filters and validators can still append directly through a retained SDK store; appends before `Begin` have no command origin and are outside immediate-append observation. Before the first operation is entered, Arc asks the integration for a read-only snapshot of persistence facts. If a handler already caused a confirmed, unknown or mixed immediate append, Arc refuses to enter any operation and the command fails with `commands.ErrInvalidOperation`.

A rejected handler append is `NotCommitted`, not an uncertain outcome. Even though its rejection poisons the transaction, Arc may still enter the returned operations, then roll back deferred work and compensate those operations against the known final facts. Inspect direct append results and return the failure from the handler to avoid that execute/compensate cycle.

The integration fails closed when missing observation is detectable. Without an `AppendObserver`, such as a custom `Options` without `Appends`, operation commands fail with `ErrUnsupported` before any operation is entered; ordinary commands are unaffected. The SDK adapter always installs the observer, but attribution still depends on configuring `sdk.ResolveAppendOrigin` when you construct the client, before `Capture` in shared-provider setups. Arc cannot detect a client built without it, so an immediate append from such a client looks like no work and can be compensated wrongly.

:::caution[Explicit aggregate commits are refused in operation commands]
`AggregateRoot.Commit` returns `commands.ErrInvalidOperation` in an operation command, before any commit RPC. The refusal poisons the shared owner, so the command fails, returns no response, enters no operation and persists nothing, even if the handler ignores the error. Return the events and let the terminal step commit them. Ordinary commands keep explicit commit.
:::

Compensation is a new write, never an unappend. A compensator receives its dependency bundle from planning time. Borrowed SDK facades in that bundle, such as the `*chronicle.EventStore` or an event sequence, stay usable while the client is open. The command's transaction, unit of work and aggregate roots are already completed and must not be reused. Check each compensating append's own result; the original completion report does not describe it. Arc never retries an operation, a compensator or a commit.

## Inject keyed read models

Register a model with Chronicle, bind its typed declaration with `sdk.BindReadModel`, then install the integration. The frozen selected-store catalog must identify its projection or reducer. An Arc query declaration alone does not grant Chronicle ownership. `sdk.BindExternalReadModel` is an explicit assertion of an external producer.

Inside a handwritten `commands.Invoke` or `commands.Prepare` adapter, use `commands.RequireReadModel[M](ctx, invocation)`, `ReadModelOrNil[M]`, or presence-bearing `ResolveReadModel[M]`. Ordinary domain methods can keep their model parameter. The current generator does not yet emit these integration-specific parameter adapters.

Missing/blank keys fail even for optional injection. A valid key with no model gives nil for optional reads and `dependencyUnavailable` for required reads. A present zero-valued model remains present. Successful reads, including absence, are cached per frame, model, key and coordinates; Provide and Handle reuse them, while child frames do not. Failures are never cached as absence.

The SDK typed reader owns codecs, collection normalization and its admitted release boundaries. Do not generalize materialized keyed-read behavior to legacy watches, windows or local reducer notifications; their release ownership remains unresolved in Chronicle.Go #35. Snapshot progress is diagnostic, not protected-decision evidence; decide from [Chronicle decision reads](decision-reads.md) instead. A materialized projection can lag. Validation filters may read models without starting transaction participants or running Provide/Handle.

## Make aggregate decisions

An aggregate directly embeds `*integration.AggregateRoot`. `DefineAggregate` accepts its constructor and typed `OnAggregateEvent` callbacks. `factory.Get(ctx, invocation)` resolves it by the original command key. Re-resolve for each callback; old root capabilities expire, and aggregate objects must not be used concurrently or retained beyond the command.

History is loaded once per root, aggregate type, source and route. Empty history enrolls `NoMatchingEvent`; otherwise the check uses the last **loaded** position, including a real position zero. No later tail read replaces that expectation. `Apply` stages before folding local state; a failing fold poisons the transaction even if its caller ignores the error. `Failed(message, validation.Error)` blocks automatic commitment. Warnings and information do not independently block persistence.

The default stream type is the aggregate's simple type name. `WithAggregateRoute` overrides routing; `ConfigureCommand.Sequence` selects the shared command sequence. History and dispatch must agree. Unknown generations and missing handlers fail explicitly. History is buffered, not a bounded-memory stream.

Returned-event snapshots and aggregate history use the selected store's frozen SDK event descriptors, preserving explicit JSON names, configured naming and field codecs. Each historical event uses its exact registered ID and generation; register that historical shape and its aggregate handler. The adapter does not guess a current shape, use alternate-generation content or migrate locally. A decode failure returns the SDK error and prevents folding any of that history.

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

## Share a captured client with one provider

Keep ordinary `chronicle.NewClient` and constructor/closure wiring when you do
not need a service provider. A container is not required by Arc or its Chronicle
adapter. When preparation collaborators need the same client that Arc handlers
will resolve, capture its identity before building your immutable provider.
Prepare it only after all bindings are registered, then create the adapter.

The following setup excerpt comes from the compiled
[shared-provider example](https://github.com/Cratis/Arc.Go/blob/develop/integrations/chronicle/sdk/composition_example_test.go).
It runs inside an error-returning function with a named `err` result and a `ctx`.
It assumes a registry with an unclassified event and a
`RegisterSeederFactory[*exampleCompositionSeeder]` declaration. That example's
seeder holds a borrowed `*chronicle.Client` and implements
`Seed(*seeding.Builder) error`. Imports use `di` for Fundamentals'
`dependencyinjection` package and `container` for its optional default container.
Configure connection/authentication options at Capture for runtime use.

<!-- shared-provider-sequence -->

```go
preparation, err := chronicle.CaptureClient(
    chronicle.WithRegistry(registry),
    chronicle.WithAppendOriginResolver(sdk.ResolveAppendOrigin),
)
if err != nil {
    return err
}
var provider di.Provider
defer func() {
    // No work/observers in this setup example. Runtime users drain Arc first.
    err = errors.Join(err, preparation.Client().Close())
    if provider != nil {
        err = errors.Join(err, provider.Close(ctx))
    }
}()
var bindings container.Registry
if err := di.BindValue(&bindings, preparation.Client()); err != nil {
    return err
}
if err := di.BindFunc1(&bindings, di.Singleton, func(_ context.Context, client *chronicle.Client) (*exampleCompositionSeeder, error) {
    return &exampleCompositionSeeder{client: client}, nil
}); err != nil {
    return err
}
provider, err = bindings.Build()
if err != nil {
    return err
}
client, err := services.PrepareClient(ctx, preparation, provider)
if err != nil {
    return err
}
adapter, err := sdk.New(client, sdk.Config{Store: "example", OwnClient: false})
if err != nil {
    return err
}
builder, err := arc.NewBuilder(arc.Options{ScopeFactory: provider, DependencyCatalog: provider})
if err != nil {
    return err
}
if err := adapter.Install(builder); err != nil {
    return err
}
```

`BindValue` borrows the exact captured pointer. `PrepareClient` returns that same
pointer, and Arc borrows the same provider through both `ScopeFactory` and
`DependencyCatalog`. Use singleton lifetimes for client-lifetime collaborators;
preparation's scoped resources are disposed before preparation returns. The
example then registers a handwritten typed command, builds and starts Arc
without connecting. It prints `prepared without connecting`.

Do not combine captured `services.WithServices` with an explicit provider passed
to `PrepareClient`: those are conflicting preparation scope configurations.
Before or during preparation, actual `sdk.New` returns `ErrNotPrepared` without
poisoning the admitted preparation. A concurrent second preparation returns
`ErrPreparationInProgress`. Preparation retains one completed outcome and does
not rerun its callbacks. Closing the client from a preparation callback or its
cleanup returns without self-wait, but publishes `ErrClosed`, not a usable
integration. This is not permission to close from arbitrary SDK runtime callbacks.

The no-transport fixtures use a real lazy gRPC connection to check zero dials,
RPCs, streams and origin resolutions during Capture, Prepare, adapter creation,
Install and Build. Preparation is **not** Connect, registration, observer
attachment or catch-up. The kernel witness separately verifies shared handler
identities, the captured resolver's actual callback context and exact nonzero
command origin. A confirmed immediate append followed by an intentional handler
failure retains the original error and a `Committed` completion, with persisted
kernel readback; it cannot be rolled back by the later failure.

This bounded consumer uses Chronicle SDK
`v0.0.0-20261003210034-9e0c8d5ba5f1`, Arc `435a136792b6`, Fundamentals `v0.1.0`
and Go 1.26 minimum. The SDK baseline includes fail-closed profile changes beyond
two-phase construction. It does not establish classified decision, compliance,
watch or replay safety. Legacy watch/window/local-reducer release ownership is
still tracked by [Chronicle.Go #35](https://github.com/Cratis/Chronicle.Go/issues/35).

## Wire lifecycle and audit deliberately

Construction and Build perform no network I/O. Start Arc admission, bind reactor
command bridges, then call `integration.Start(ctx)` for configured
`StartupNamespaces`. Begin HTTP serving only after required readiness succeeds.
Default clients and supplied providers are borrowed. `OwnClient: true` transfers
client closure to an explicit, once-only `integration.Close()` call; it is not a
late automatic Arc shutdown hook.

For an application-owned client/provider, stop producers first, unregister and
join any owned observers, then drain/join Arc with `app.Shutdown(ctx)`. Close the
borrowed integration, close/join the client's runtime, join any outstanding
`PrepareClient` call including its resource cleanup, and only then close the
provider. Client cancellation alone is not a preparation join. A short Arc
shutdown deadline leaves the application Stopping: retain the provider while
admitted commands still hold resources, release/join them and call Shutdown
again before provider disposal. The composition fixture needs no observer.

Provider disposal does not close a `BindValue` client. The isolated no-transport
ownership test disposes an idle provider first and then probes real transport to
prove the SDK was not closed; this is an ownership test, **not** the normal
shutdown order above.

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

Integration-tagged tests fail when the endpoint variable is absent. They exercise HTTP commands, atomic rejection/readback, tenants, shared projection/query models, projection injection, aggregate competition, reactor-returned commands, failed observer partitions, ignored immediate-append rejection, operation recovery for every completion, including a lost acknowledgement, and protected decision reads. Root and tools gates run separately; `./...` does not cross module boundaries.

Watches, aggregate snapshots, automatic historical-generation migration, full compliance authoring, generated operation adapters, nested operation workflows and durable operation recovery remain unsupported. None is implied by an ordinary injected model or a successful snapshot test.
