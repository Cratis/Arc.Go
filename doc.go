// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package arc is the composition entry point for Cratis Arc for Go.
//
// NewBuilder composes explicit typed command/query registrations, authentication,
// identity and discovery. Build is single-attempt and activates no dependencies;
// Start activates explicit lifecycle hooks. Application implements http.Handler
// for embedding, or owns a listener through Serve and Run. Shutdown closes admission,
// drains work and stops hooks in reverse order. Callbacks must honor context.
//
// Builders are single-owner construction state. Built applications support
// concurrent requests when borrowed collaborators do. Ordinary middleware and
// providers are trusted code, not a sandbox. No framework container is required.
//
// Importing Arc starts no work or I/O. The hosted HTTP surface serves commands,
// snapshot queries and observable queries: direct Server-Sent Events and
// WebSocket streams plus the multiplexed SSE and WebSocket query hubs. Snapshot
// HTTP CQRS does not require event sourcing.
//
// Code generation, Chronicle and MongoDB live in separate nested modules that are
// not part of this module and are versioned on their own:
// github.com/cratis/arc.go/tools provides arc-gen, which emits registration
// adapters, TypeScript proxies, OpenAPI documents and Screenplay output, and
// github.com/cratis/arc.go/integrations/chronicle and
// github.com/cratis/arc.go/integrations/mongodb provide the optional adapters.
//
// Start with the [documentation], read [observable queries] and [code generation]
// for streaming and generated clients, and use the [parity ledger] for the exact,
// bounded contracts and the differences from C# Arc.
//
// [documentation]: https://github.com/Cratis/Arc.Go/blob/main/Documentation/index.md
// [observable queries]: https://github.com/Cratis/Arc.Go/blob/main/Documentation/backend/go/queries/observable-queries.md
// [code generation]: https://github.com/Cratis/Arc.Go/blob/main/Documentation/backend/go/generation/index.md
// [parity ledger]: https://github.com/Cratis/Arc.Go/blob/main/Documentation/parity.md
package arc
