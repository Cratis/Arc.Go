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
// Importing Arc starts no work or I/O. Observable HTTP, OpenAPI, generated browser
// proxies and Chronicle integration remain unsupported. Snapshot HTTP CQRS does
// not require event sourcing. See Documentation/parity.md for bounded contracts.
package arc
