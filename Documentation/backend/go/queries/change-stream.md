---
title: Collection change streams
description: Choose full, delta or legacy transfers and understand identity, privacy and delivered baselines.
---

Sending a large list after every small change wastes bandwidth. Hub collection
transfers can send only the added, replaced and removed items after the first
snapshot. Direct SSE/WebSocket and Go `Subscribe` still send complete results.
Actual JavaScript/React reconstruction has not been executed against this branch;
server wire evidence is not a browser compatibility claim.

## Choose the transfer mode

| Hub mode | First delivered result | Later delivered results |
| --- | --- | --- |
| `full` | Full data, no change set | Full data, no change set |
| `delta` | Full data, no change set | Change set, data omitted |
| `legacy` | Full data plus added-all change set | Full data plus change set |

Missing/unknown textual modes select legacy. Single models and collections without
an eligible identity always use full snapshots without change sets. Empty declared
collections retain their item type; emptiness does not disable identity metadata.

A change set has three required arrays. Removed entries are complete previously
delivered items, not just IDs:

```json
{"added":[{"id":"c","title":"new"}],"replaced":[{"id":"a","title":"changed"}],"removed":[{"id":"b","title":"gone"}]}
```

An unchanged collection sends empty arrays. Reordering alone is not a positional
change. Clients reconstruct by identity, not array index. The wire does not promise
that delta reconstruction will reproduce a provider-only reorder.

## Keep server and client identity compatible

Registration compiles case-insensitive exported Go `ID`/`Id` member extraction.
It does not reuse command `arc:"key"` metadata. A custom
`WithCollectionIdentity[A,T](extract)` uses a typed `func(T) (any, error)`;
unsupported identity encoding fails rather than publishing partial differences.

Emit a client-compatible JSON identity, conventionally `id`. A custom server
extractor does not teach the stock JavaScript client a differently named property.
Null identities are skipped on the identity path. Duplicate IDs use their last
value with deterministic first-encounter ordering.

`queries.ComputeChanges(previous, current)` also supports identity-less JSON-set
comparison: additions/removals, never replacements. That standalone fallback is
**not** the hub's identity-less transfer policy; hubs send full snapshots instead.

## Commit only delivered state

The baseline is the last successfully delivered, intercepted snapshot, not the
latest source value. The pipeline prepares immutable serialized candidates,
reserves retained bytes, and commits only after the delivery callback succeeds.
Hub callbacks wait for local writer acknowledgement. Queue admission, suppression,
stale ownership, cancellation, encoding errors and failed writes never advance it.
Cancellation after candidate reservation releases that candidate too.

Local successful write/flush is not browser acknowledgement, durable receipt or
exactly-once delivery. On replacement or reconnect, the first delta result is full
again. There is no replay/resume token. Removed items contain only the previously
delivered masked fields; neither provider mutation nor callback mutation can
rewrite that baseline.

Candidate and previous baseline coexist until acknowledgement and both count
against retained-byte budgets. `ObservationOptions.MaxBaselineBytes` defaults to
16 MiB; hubs use `HTTP.MaxResponseBytes`. `ReserveBaseline` is an optional host
reservation callback returning an idempotent release function. Connection and
application budgets also include queued/in-flight frames. Oversize or exhaustion
fails explicitly; there is no silent eviction/coalescing.

## Supply known-change hints conservatively

An observable may emit a **value** `queries.ObservedCollection[T]` with:

| Field | Meaning |
| --- | --- |
| `Items` | Complete current `[]T`, rendered as query data |
| `Changes` | Optional `CollectionChange` identities and kinds |
| `Version` / `PreviousVersion` | Source-local continuity hints |
| `Generation` | Source generation for continuity |

`CollectionAdded`, `CollectionReplaced` and `CollectionRemoved` describe the hint
kind. Versions/generation are server metadata, never wire subscription revisions.
Zero versions or an empty generation disable continuity hints. Pointer wrapper
declarations are rejected; declare `observable.Source[queries.ObservedCollection[T]]`.

Hints are compared with the complete intercepted diff. Missing changes, broken
predecessor/generation continuity, suppressed versions or transformations that
invalidate hints fall back to ordinary diff computation. Unknown hint identities
cannot hide an independently detected change. This is conservative correctness,
not a claimed faster algorithm.

Select authorized rows before paging/counting and apply
[interception and guards](observable-emission-guards.md) before transfer. Chronicle
and MongoDB watch adapters are not implemented; do not synthesize authoritative
read-model watches from command append notifications.
