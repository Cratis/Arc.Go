---
title: Generate constructor service registrations
description: Opt in to constructor bindings, compose them separately from Arc artifacts, and preserve lazy activation and resource ownership.
---

When service constructors already describe your dependencies, you can generate
their registrations instead of repeating factory closures. The experimental
`arc-gen -bindings-config` option adds `RegisterServices` alongside the existing
artifact adapters. Plain constructors, manual registration closures, and
`ArcBindings` callbacks remain supported; using a container is optional.

## Before you start

- Use Go 1.26 or later and the [source-installed generator](index.md#run-the-tool-from-source).
  Pin its source revision. The tools module and compiled consumer fixture use
  Arc.Go `v0.0.0-20261003142617-78ebbf8fdff8` (the immutable `78ebbf8` revision)
  and Fundamentals.Go `v0.2.0`, without a workspace or local replacement.
  The Arc version is a pseudo-version, not a release tag.
- Select the main-module packages containing your declarations and constructors.
  Choose **one** of those packages as the service-registration output owner.
- Keep composition code that calls generated symbols outside the selected
  packages. Analysis hides old generated output so stale declarations cannot
  prevent regeneration; handwritten inputs must compile without it.

The excerpts below come from the external consumer fixture in
`tools/internal/artifacts/testdata/servicebindings`. They are not a standalone
application: the [declarations](../../../../tools/internal/artifacts/testdata/servicebindings/input.go.txt)
include the counters, other services, and command/query types used by the
[composition and lifetime tests](../../../../tools/internal/artifacts/testdata/servicebindings/contract_test.go.txt).
The example module's import path is `example.test/consumer`.

## 1. Keep constructors ordinary

This declaration excerpt supplies a scoped concrete service and an interface:

```go
//cratis:scoped
type Foo struct{ Closes int }

func NewFoo() *Foo          { Calls++; return &Foo{} }
func (f *Foo) Close() error { f.Closes++; return nil }
func (*Foo) Name() string   { return "foo" }

type IFoo interface {
    Name() string
    Close() error
}
```

With constructor selection omitted, the shared planner finds package-level
`NewX` functions associated with a local named `X`. It scans only your selected
packages, never recursively discovers constructors through imports, and never
runs package initialization or application code. There is no richest-constructor
selection or automatic pointer conversion. Multiple selected constructors for
one exact key are an error.

A constructor returns a named service `T` or `*T`, optionally followed by the
predeclared `error`. It may take one leading `context.Context`; remaining
parameters are exact dependency keys. Generic functions, open types, variadics,
pointer-to-interface results, and constructors for marked command DTOs are
rejected. Commands continue to be decoded request models.

Lifetimes default to transient. Put `//cratis:singleton`, `//cratis:scoped`, or
`//cratis:ignore-convention` on the named type's doc comment, not its constructor.
Grouped declarations need per-type comments. These directives belong to
Fundamentals' shared `bindingtypes.ReadDirectives` and `Analyze` planner, not a new
Arc annotation family. Unknown, misplaced, repeated, or conflicting directives
fail generation. Ignoring a service convention does not disable Arc artifact
discovery.

## 2. Declare ownership and existing services

Save the fixture's [bindings configuration](../../../../tools/internal/artifacts/testdata/servicebindings/bindings.json)
as `/work/consumer/bindings.json`, with the full fixture declarations in that
module:

```json
{
  "formatVersion": 1,
  "package": "example.test/consumer",
  "interfaces": [
    {"service": "IFoo", "implementation": "*Foo"},
    {"service": "ISingleton", "implementation": "*Singleton"},
    {"service": "ITick", "implementation": "*Tick"}
  ],
  "existing": [
    {"key": "*Dependency", "lifetime": "singleton"},
    {"key": "*External", "lifetime": "singleton"}
  ],
  "requireAllDependencies": true
}
```

The explicit interface pairs select exact concrete keys. Alternatively,
`matchIFoo: true` enables same-package `IFoo`/`Foo` matching. It is off by default,
and competing structural implementations are diagnosed even if a competitor has
no constructor. Explicit pairs avoid guessing which implementation you intend.

Every `existing` entry is your attestation that composition supplies that exact
key with its **actual** lifetime. Register those values before calling
`RegisterServices`. If the registrar implements `di.Catalog`, generated code
checks presence without resolving anything. Catalog does not inspect lifetimes;
without it, your composition must guarantee presence itself. An attestation does
not create a service or transfer its ownership.

For a second generator or manual registration to own a key, list that key and
lifetime in `existing`. If Arc's plan also selects it, set
`"duplicates": "keepExisting"`; only explicitly listed keys are retained, and
Arc emits no registration for them. Register the owner first. Chronicle generator
integration is outside this one-owner composition workflow. Unlisted overlaps
and registrar errors still fail.

## 3. Generate and check

From the Arc.Go checkout's `tools` directory, with `/work/consumer` prepared as
above, run:

```sh
GOWORK=off GOTOOLCHAIN=local go run ./cmd/arc-gen -dir /work/consumer -bindings-config /work/consumer/bindings.json .
GOWORK=off GOTOOLCHAIN=local go run ./cmd/arc-gen -dir /work/consumer -bindings-config /work/consumer/bindings.json -check .
```

Flags precede package patterns. Replace `.` with the complete selected package
scope when your constructors span packages; the configured owner must be among
those packages. Dependencies may be referenced through compiler imports, but a
foreign constructor itself must belong to a selected package.

Successful generation reports service bindings published and explicitly retained,
with the owner's import path. The owner receives `RegisterServices` in
`zz_arc_generated.go`; artifact packages retain `RegisterArtifacts` and
`ArcBindings`. A service-only owner also emits real registrations. `-check`
reports verified bindings only when output matches, and never writes or repairs
files. Commit the generated files with their inputs.

## 4. Compose services separately from artifacts

This composition excerpt adapts the fixture's test setup. It belongs outside the
selected package and assumes a test's `t *testing.T`, imports of `context`,
`example.test/consumer` as `consumer`, `github.com/cratis/arc.go` as `arc`, and
Fundamentals' `dependencyinjection` as `di` and `dependencyinjection/container`.

```go
r := &container.Registry{}
if err := di.BindValue(r, &consumer.Dependency{}); err != nil {
    t.Fatal(err)
}
external := &consumer.External{}
if err := di.BindValue(r, external); err != nil {
    t.Fatal(err)
}
if err := consumer.RegisterServices(r); err != nil {
    t.Fatal(err)
}
p, err := r.Build()
if err != nil {
    t.Fatal(err)
}
t.Cleanup(func() {
    if err := p.Close(context.Background()); err != nil {
        t.Error(err)
    }
})
builder, err := arc.NewBuilder(arc.Options{ScopeFactory: p})
if err != nil {
    t.Fatal(err)
}
if err := consumer.RegisterArtifacts(builder); err != nil {
    t.Fatal(err)
}
app, err := builder.Build()
if err != nil {
    t.Fatal(err)
}
```

The excerpt stops at composition; use `app` in the rest of your test and shut it
down before the provider cleanup runs. Your host owns `app.Start`, `app.Shutdown`,
and `p.Close`; close the provider after application shutdown has joined work, and
check cleanup errors. `di.BindValue` supplies borrowed singletons: the application
still owns `external` and any other external resources and their cleanup.

Generation, both registration functions, provider Build, and application Build
leave constructors inactive. Provide resolves its services only when preparation
runs; Handle resolves its services only after preparation accepts. Denied work,
validate-only commands, a stopping Provide, and failed query argument binding do
not activate the later business factories. Policies and validators may resolve
their own stage dependencies; this is not a promise that all authorization or
validation work is dependency-free.

You can still supply `ArcBindings` callbacks for individual artifact dependencies,
or use callbacks exclusively without registering constructor services. Callbacks
remain stage-local and borrow their returned values. Constructor graph edges do
not become every command's dependency manifest. See
[artifact registration](index.md#register-the-package-once) for callback ownership
and concurrency requirements.

Constructor results are owned by the provider. Interface forwarders borrow the
concrete result and use its effective lifetime, avoiding a second close for the
same instance. The generator adds no caches, close loops, or lifecycle workers.
It preserves the construction context and actual result returned alongside an
error, so failed owned results can be disposed. Dependency failure before the
constructor runs must not dispose a nonexistent zero-valued result: one through
four dependency arguments use the safe `BindFunc1`–`BindFunc4` adapters; zero uses
`di.Bind`. Larger constructors resolve ordered arguments separately and declare
unique direct edges, but disposable **value** results above arity four are
rejected. Repeated transient arguments remain separate resolutions.

Check every registration error and discard partial registry/builder composition
on failure. Registration is not transactional or idempotent. The container's
Build remains authoritative for missing edges, cycles, and singleton-to-scoped
captures; generator success is not a substitute for building the provider.

## Configuration reference

The bindings file is separate from the application/TypeScript `-config` profile.
Without `-bindings-config`, constructor generation is disabled and ordinary
adapter bytes and callbacks are unchanged.

| Field | Default | Contract |
| --- | --- | --- |
| `formatVersion` | Required | `1` |
| `package` | Required | One exact selected main-module import path; output owner |
| `constructors` | Omitted | Discover exact `NewX` functions in selected packages; `[]` selects none; a nonempty list selects exact function references; `null` is invalid |
| `matchIFoo` | `false` | Opt in to same-package interface naming convention |
| `interfaces` | Empty | Pairs of `service` interface and exact `implementation` key, including pointer shape |
| `existing` | Empty | Entries require `key` and actual `lifetime`: `singleton`, `scoped`, or `transient` |
| `duplicates` | `reject` | `keepExisting` retains only explicitly attested overlaps; it is not a general try-add policy |
| `requireAllDependencies` | `false` | Promote missing non-scalar dependency obligations from information to errors; scalar configuration always requires explicit provider attestation |

References are local `Name` or fully qualified `import/path.Name`, with an optional
`*` on type keys. Qualified names address selected packages and their direct
compiler imports; configuration does not load extra packages. Aliases preserve
exact Go identity. Name a closed generic or composite key through an accessible
alias, as the foreign-constructor fixture does with
`type Key = Box[[]*Dependency]`; do not put that expression in JSON. Foreign
signatures must be accessible from the output owner. Bare predeclared type keys
such as `string` are accepted for explicit configuration providers.

Unknown or duplicate JSON members, trailing data, invalid references, and
unsupported versions fail. Shared planner diagnostics retain their `BT` codes:
for example, `BT011` identifies a missing dependency obligation, `BT012` missing
scalar configuration, and `BT013` an explicitly retained registration.

## Resolve generation failures without losing output

All selected packages are analyzed and rendered before publication. Preflight
checks generated symbol collisions (`ArcBindings`, `RegisterArtifacts`, and
`RegisterServices`) and the complete generated service import set, including
aliases, nested generic arguments, existing keys, and forwarders. Direct or
transitive import cycles, importing a `main` package, and Go `internal` visibility
violations fail before any output changes. Fix the declarations or selection and
rerun the same command.

Services use the existing `zz_arc_generated.go` writer and ownership rules, with
no second service manifest. Removing the opt-in removes selected stale owned
service-only output. Unselected or foreign-owned output is left alone. Mixed
Go/TypeScript publication requires manifest ownership and never adopts legacy
adapter files, even if their marked bytes match. Keep one writer per output
scope and follow [ownership and recovery](typescript.md#ownership-and-recovery)
for interrupted mixed publication. Adapter-only writes are atomic per file,
not across packages.

This opt-in covers constructor authoring, not implicit application-wide discovery,
standalone policy/validator discovery, or operation-adapter generation. Those
broader surfaces remain partial. The internal wire-contract graph is also not
OpenAPI document publication: Generate still refuses OpenAPI output. Consult the
[parity map](../../../parity.md#generation-ledger) for the supported boundaries.
