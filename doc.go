// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package arc is the composition entry point for Cratis Arc for Go.
//
// The foundation packages provide explicit artifact metadata, conventional routes,
// portable scalar concepts and command/query/validation result envelopes compatible
// with the corresponding C# Arc contracts. See metadata, concepts, serialization,
// commands, queries and validation for the implemented APIs.
//
// Command/query execution, HTTP hosting, authentication and Chronicle integration
// are not implemented yet. Importing this package starts no services and performs
// no I/O. Arc's HTTP CQRS contracts do not require event sourcing.
package arc
