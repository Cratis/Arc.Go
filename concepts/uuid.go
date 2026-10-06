// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package concepts aliases Fundamentals.Go's portable UUID and calendar scalars.
// Values are immutable by convention and safe to copy. They are not Chronicle
// event source IDs: those may be arbitrary strings.
package concepts

import fconcepts "github.com/cratis/fundamentals.go/concepts"

// UUID contains 16 bytes in RFC network order, not .NET Guid.ToByteArray order.
// Its zero value is Guid.Empty. JSON and text use lowercase dashed notation.
// It is an alias of the shared Fundamentals.Go type.
// A new named type based on UUID must explicitly forward its codec methods.
type UUID = fconcepts.UUID

// ParseUUID accepts the dashed Guid D format, including uppercase hex digits.
func ParseUUID(text string) (UUID, error) { return fconcepts.ParseUUID(text) }

// NewUUID returns a cryptographically random version 4 UUID.
func NewUUID() (UUID, error) { return fconcepts.NewUUID() }
