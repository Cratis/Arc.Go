# Arc.Go parity

The target is maximum API, behavior, and developer-experience parity with C# Arc
in `../Arc/Source/DotNET`, expressed idiomatically in Go. HTTP contracts must
remain compatible with the existing TypeScript consumers. This initial map
states the target, not demonstrated implementation parity.

## Status and evidence

- **Not implemented**: no working Go surface yet.
- **Partial**: only named behavior has executable evidence; list the gaps.
- **Implemented**: the named contract has tests that detect its regression.
- **Go-specific**: a deliberate Go shape or behavior; explain the deviation,
  rationale, compatibility impact, and executable evidence separately.

Do not promote a status from source reading, compilation, or an empty test run.
Each implemented entry must identify the C# source/revision, Go symbols, and
concrete tests. Preserve unresolved behavior explicitly.

## Initial scope

| Surface | Status | Target and evidence required |
| --- | --- | --- |
| Commands | Not implemented | Binding, validation, authorization, result tests |
| Queries | Not implemented | Routes, JSON, paging/sorting, and error fixtures |
| Observable queries | Not implemented | Framing, disconnect, and backpressure tests |
| Chronicle integration | Not implemented | Chronicle.Go adapter behavior tests |
| Overall C# framework parity | Not implemented | Surface-by-surface evidence |

## Intended Go translations

Context-first operations with final errors, explicit typed registration,
`net/http` handlers, named domain values, and constructors/options replace C#
async methods, attributes/discovery, host integration, concepts, and DI.
These are planned translations, not implemented capabilities. Add individual
entries with source revisions, rationale, exact differences, and caller migration
guidance when implementing.

Preserve camelCase JSON, routes, response envelopes, missing/null semantics,
authorization, and the original observable transport. SSE and WebSocket are not
interchangeable. Keep CQRS independent from event-sourcing requirements; the
Chronicle integration is optional for applications. Follow the
[porting rules](../.cratis/ai/rules/go-cratis-parity.md).
