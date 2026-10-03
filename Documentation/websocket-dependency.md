---
title: WebSocket runtime dependency
description: The pinned WebSocket implementation and its runtime boundary.
---

## Dependency and boundary

Arc.Go pins `github.com/coder/websocket` **v1.8.15**, the latest v1.8.x tag
returned by the Go module proxy when this dependency was introduced. The tag
resolves to `9c8faadccd1b679e811a79ce506f8a10237251ad`. Its module requires Go 1.23
and has no module dependencies; Arc.Go continues to require Go 1.26.

The standard library has no WebSocket server. This implementation supplies
context-aware complete-message reads and writes, fragmented input, message
limits, RFC control frames, and close handling. Compression is disabled.

All library imports belong to `internal/websockettransport`. The public
`observable` and `queries` packages do not import that adapter. Importing root
`arc` includes the adapter once WebSocket endpoints are wired; this is a core
transport, not an optional fourth module.

## License and security

The v1.8.15 `LICENSE.txt` grants use, copying, modification and distribution
with preservation of its copyright and permission notice (**ISC**, not MIT).
No upstream source is copied into Arc.Go. Module downloads retain that notice.
The reviewed module has no transitive dependency closure. Pin and checksum
changes remain subject to `go mod verify` and `govulncheck`; a pin does not
promise immunity from future vulnerabilities.

Arc.Go validates its exact same-origin/allowlist policy before invoking Accept.
The adapter disables the library's separate host-pattern policy only after that
host check, avoiding a second policy with different scheme and wildcard rules.
Application JSON Ping/Pong messages remain distinct from RFC control frames.

This dependency checkpoint alone does not implement a transport endpoint or
establish browser-client compatibility.
