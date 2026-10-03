---
title: Snapshot HTTP contract
description: Look up Arc.Go unary methods, status overrides, wire headers and privacy behavior.
---

## Routes and methods

Commands map POST and POST `/validate`. Default snapshot queries map GET, HEAD and
QUERY; GET-only maps GET/HEAD, QUERY-only maps only QUERY. Disabled QUERY is absent
from Allow. Unknown routes return empty 404; unsupported methods on known Arc paths
return empty 405 with lexical Allow. OPTIONS is not automatic CORS. Routes are
case-sensitive at runtime, with case-insensitive registration collision checks.

Literal root/trailing-slash query routes never become subtrees. Repeated slashes,
dot segments, backslashes and escaped aliases/separators return empty 400 rather
than redirects. Canonical percent-encoded Unicode paths are accepted. No trailing
slash is silently added or removed. Raw catch-all/subtree handlers may overlap
Arc routes, but the Arc table wins, including method mismatches. `/.cratis/*`
is reserved even when discovery is unmapped; it never falls through to raw handlers.

## Input

Missing Content-Type is accepted. Explicit application/json and application/*+json
with UTF-8 charset are accepted; unsupported media/encoding returns 415 before user
stages. Bounded body/query limits return 413. Empty/null/nonobject command bodies
are malformed. QUERY uses its own reader contract; unknown envelope fields and
unknown arguments are ignored. GET bodies are not query arguments.

X-Allowed-Severity applies only to commands: one supported signed decimal int32;
Error is capped at Warning, repeats/comma combinations are ignored, and command
floors cannot be weakened. Default policy retains Error only.

## Publication and status

Normal precedence is success 200, unauthorized 403, invalid 400, query-not-ready
202, otherwise 500. Transport overrides are credential rejection 401, reader
failure 400, oversized input 413, unsupported representation 415, unavailable
admission 503 and routing 404/405. Cancellation does not invent a 499 status.

JSON is `application/json; charset=utf-8`, encoded before commitment. Required
arrays stay `[]`; legitimate scalar zero/false/empty command responses remain
present. Failed commands omit response; failed queries omit data/change set.
Encoding failure removes unsafe payload/state, retains denial/validation precedence
and publishes a safe exception when possible. Last resort is empty 500. A failed
publication never retries a command or promises rollback of external writes.

Malformed command text is exactly:

> The request body could not be read or is not valid for this command.

Production exception text is exactly:

> An internal error occurred while processing the request. See server logs for details.

Stack detail is empty unless explicitly enabled. QUERY no-store applies to every
matched failure, including authentication and admission. HEAD executes GET,
computes status/representation length and suppresses bytes; snapshot reads never
negotiate streams or fabricate wait behavior.

## Metadata and identity

X-Correlation-ID echoes a canonical nonzero UUID and matches envelope correlation.
One valid supplied header wins, then trusted context, then generation. Repeats are
invalid input. HTTP receipt is captured before authentication/binding and consumed
by only the immediate endpoint pipeline call. Nested operations and backend
operations invoked by middleware, raw handlers or providers get fresh receipts.
Invalid tenant selectors return 400 with malformedRequest validation findings;
ordinary QUERY reader syntax exceptions retain their redacted exception envelope.
x-cratis-tenant-id selects tenant, never membership. Inherited principal/tenant
are explicitly shadowed unless a trusted authentication adapter establishes identity.

Catalogs and identity are unwrapped. `/.cratis/me` is fresh, anonymous 401 and
provider-denied 403, with no identity-cookie authentication. Presented legacy
`.cratis-identity` cookies are expired at `/`, never decoded or reissued.
Deletion matches C# RemoveCookie: empty value, expiry one day in the past, no
Domain, HttpOnly, Secure, SameSite or Max-Age attributes. It does not depend on TLS.
Go detects direct TLS through `Request.TLS`; HTTPS terminated at a proxy does not
set it automatically. Arc does not trust Forwarded or X-Forwarded-Proto headers
for TLS detection; configure a trusted host adapter if your own middleware needs
that information.
Identity/discovery use `no-store, private` and merged `Vary: Cookie`.
