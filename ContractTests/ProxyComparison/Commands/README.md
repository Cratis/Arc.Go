# Bounded Go-generated command family

These files are **Go-generated**, not new C# captures. `input.go.txt` is a
metadata-only analyzer fixture; its Handle bodies are not executed by the renderer.
The internal `renderTypeScriptCommands(*Graph)` seam returns the complete sorted
in-memory command/model/barrel family, or nil/error. Public CLI TypeScript
publication remains disabled. There is no query or observable emitter here.

The 14-file inventory is eleven commands (Register, Echo, Count, Toggle, Identify,
Single, Many, Numbers, Guarded, Rules, CreateTask), two response models (Detail,
Created), and one barrel. Register's raw Handle return is consumed server-side:
its explicit finalized `response=none` produces no Detail client response.
Outcome wrappers supply finalized string/number/boolean/Guid/model/list metadata.

## Authority and differences

Authority is Arc commit `7c1e78075b737df64f69fddfaae83374f75e3612`,
`Source/DotNET/Tools/ProxyGenerator/Templates/Command.hbs` and
`CommandDescriptor.cs`, plus the unchanged `Primary22.48.2/ProxyComparison/Register.ts`
capture. Compilation and runtime use the existing exact lock: Arc and Arc.React
22.48.2, Fundamentals 7.22.0, React 18.3.1, TypeScript 5.9.3. No ambient substitute
APIs, serializer, or React implementation is used.

C#-shaped optional content interfaces, setters/propertyChanged, descriptors,
undefined initial backing values, response constructors/enumerability, roles,
warning policy and static hook tuple are retained. Zero/false are not fabricated
as command defaults. Explicit command defaults diagnose until their portable
wire-value contract exists. Literal model-bound routes have no request parameters.

Intentional mechanical differences: type-only imports, deterministic indexed
private backing names (so quoted wire names work), and three explicit useCommand
generics instead of C#'s ts-ignore. `commandName` adds diagnostic FQN metadata;
the transport still uses the finalized route. Fields are registered with the
actual six-argument `Fields.addFieldToType`, because the pinned standard `@field`
decorator does not accept accessors. Server authorization is not implemented or
weakened by frontend role hints.

The pinned C# template's enumerable-response generic uses the **element**, even
though the runtime returns a list. Many and Numbers preserve this mismatch;
consumer.ts characterizes it with an expected compile failure for a corrected
array type, and runtime tests assert actual arrays. This is not an array-annotation
fix or a claim that those declarations provide typed list results.

## Evidence and checks

Ordinary Go tests compare all output bytes and exact inventory; they never invoke
Node. Explicit fixture-only regeneration from tools:

```sh
ARC_UPDATE_COMMAND_FIXTURE=1 GOWORK=off GOTOOLCHAIN=local go test -count=1 -timeout=2m ./internal/artifacts -run '^TestTypeScriptCommandFixture$'
```

From ProxyComparison, compile real tsc JS in both decorator modes and run it in
independent processes (no esbuild hygienic renaming):

```sh
npm ci --ignore-scripts
./node_modules/.bin/tsc -p Commands/tsconfig.json --noEmit false --outDir ../../.ai-work/output/ts-command/tsc-standard
node --test --test-timeout=30000 Commands/commands.test.mjs
./node_modules/.bin/tsc -p Commands/tsconfig.legacy.json --noEmit false --outDir ../../.ai-work/output/ts-command/tsc-legacy
ARC_COMMAND_DECORATORS=legacy node --test --test-timeout=30000 Commands/commands.test.mjs
```

The runtime loader resolves extensionless tsc relative imports and maps package
imports to the same locked node_modules for root-level scratch output; it does
not transform JS or replace runtime packages. Commands/consumer.ts is compile-only,
not executed. Static hook tuples compile, but no mounted React behavior is claimed.

The fetch-controlled tests are explicitly **client-unit evidence**, including
independent complete envelopes, payload selection, base paths/headers, validate,
server denial, local validation/severity, scalar/model/list hydration, callbacks,
initial values/clear/revert, and undefined defaults. They are not backend evidence.

A single actual Arc taskboard command is tested separately, with complete real
validate/execute envelope expectations. The existing host is reused unchanged:

```sh
# From repository root:
GOWORK=off GOTOOLCHAIN=local go build -o .ai-work/output/ts-command/taskboard-host ./ContractTests/taskboard/host
# From ProxyComparison:
node --test --test-timeout=30000 Commands/roundtrip.test.mjs
```

The test starts an ephemeral loopback listener, applies bounded readiness/request/
shutdown deadlines, and joins the owned process. It is one Go-host round-trip,
not all-host or paired .NET conformance.

## Limits

Portable projection is bounded to notNull/notEmpty, string minLength/maxLength/
length, and numeric greaterThan/greaterThanOrEqual/lessThan/lessThanOrEqual.
The shared independent `rule-cases.json` is evaluated by actual Go NewPortable and
JS rules (UTF-16 lengths, null presence, empty collections and boundary numbers).
Register additionally covers whitespace-only notEmpty, required-tag collection
presence, and warning severity. Rule messages/property names/severity are retained;
client PropertyRule does not manufacture Go's reason metadata.

Format/regex rules, server-only/dynamic/optional/concept rule semantics, message
PropertyName substitution, explicit command defaults, unknown responses, direct
nullable scalar/model responses, dictionary responses, nested/nullable collection
elements, command-as-model references, and runtime-member collisions diagnose.
Other inherited model limits still apply. Roles remain independent UI hints.
No arbitrary validators or constructors/codecs execute during generation.

No safe mixed-output filesystem publisher, new CLI/profile success mode, CI job,
query/observable family, mounted React test, fresh expanded NuGet capture, or
full parity/release-readiness claim is included. See Symbol.md for the inherited
standard-decorator binding fix and actual tsc-model runtime regression.
