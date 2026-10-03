# Observable protocol fixture provenance

These independent expectations target Arc C# and JavaScript revision
`7c1e78075b737df64f69fddfaae83374f75e3612`. They are source-derived, not output
captured from the Go implementation. Paths below are relative to the Arc
repository. JSON object order is immaterial except in direct SSE byte fixtures;
array order, missing fields, nulls, revisions, and numeric values are significant.
Connection and correlation identifiers are deterministic fixture identifiers.
They must be validated before normalization in live tests.

## Protocol sources

- `Source/DotNET/Arc.Core/Queries/ObservableQueryHubMessage.cs`: PascalCase
  message types, optional fields, Connected capabilities, Unix millisecond Ping/Pong.
- `Source/DotNET/Arc.Core/Queries/ObservableQuerySubscriptionRequest.cs`:
  string-or-null arguments, flat paging/sorting, textual transfer modes.
- `Source/DotNET/Arc.Core/Queries/ObservableQueryHttp.cs`: pending/current/wait,
  case-insensitive controls, timeout/completion text.
- `Source/DotNET/Arc.Core/Queries/ClientObservableSSE.cs`: direct complete
  QueryResult SSE framing and no hub envelope.
- `Source/DotNET/Arc.Core/Queries/ClientObservable.cs` and
  `Source/DotNET/Arc.Core/Queries/WebSocketConnectionHandler.cs`:
  direct Data wrapper and application Ping/Pong.
- `Source/DotNET/Arc.Core/Queries/ObservableQuerySubscriptionState.cs` and
  `ObservableQuerySubscriptionStates.cs`: owner identity and revision transitions.
- `Source/DotNET/Arc.Core/Queries/ObservableQueryDemultiplexer.cs`:
  hub transport, collection eligibility, delivery baselines, enumerable handling.
- `Source/JavaScript/Arc/Globals.ts`, `queries/WebSocketHubConnection.ts`,
  `queries/ServerSentEventHubConnection.ts`, and `queries/reconcileQueryData.ts`:
  actual consumer defaults, subscribe/unsubscribe, and collection reconstruction.

## Findings affecting the Go port

1. The default JavaScript transport is one multiplexed WebSocket connection,
   with delta transfer, not direct SSE.
2. JavaScript unsubscribe carries the existing subscription revision. Equality
   cancels; it is not stale. A greater revision also cancels.
3. JavaScript can send a legacy Subscribe before processing Connected. Its
   revision-aware replacement must supersede that temporary legacy owner.
4. C# tombstones expire after two minutes and excess entries above 1,024 are
   evicted. Go's approved policy retains bounded high-water marks for the physical
   connection lifetime and rejects new IDs at capacity; no silent eviction.
5. C# hub collections without identity fall back to full snapshots. Standalone
   JSON-set comparison is not the hub's identity-less delta policy.
6. Direct C# subject transports send full QueryResults, not negotiated deltas.
7. C# hub async-enumerable handling does not use the complete subject interception
   path. Go must use uniform interception and guards; this is deliberate hardening.
8. Go's existing query finalizer unconditionally sets Ready. It must preserve an
   authorized, valid, exception-free pending result without claiming success.
9. Go's existing shutdown drains requests before stopping hooks. Observable work
   must be canceled and joined before ordinary draining and hook disposal.
10. Go's existing HTTP WriteTimeout defaults to 30 seconds. Streaming clears that
    absolute deadline and bounds individual writes instead.

## Security and lifecycle deviations

Anonymous SSE controls require per-connection ownership evidence, unlike C#'s
indistinguishable anonymous owners. Tenant presence is part of ownership.
Malformed controls fail explicitly. Source/write failures never fail an
application-owned shared subject. A safe terminal query error may be delivered
before closing direct streams. A local successful write/flush is delivery, not a
browser acknowledgement. Reconnect starts fresh revision state and a full baseline.

Fixtures pin contracts; they alone do not establish browser compatibility.
Generator adapters, Chronicle watches, and MongoDB watches require separate evidence.
