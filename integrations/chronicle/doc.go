// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package chronicle integrates Arc commands with ordered Chronicle transactions,
// read models and aggregates through SDK-independent ports. Import the sdk child
// package to adapt Chronicle.Go. The application owns client startup and shutdown.
package chronicle

import "github.com/cratis/arc.go/commands"

// CommitResult retains persistence facts independently of operation success.
// Positions, when present, follow the entire shared transaction's event order.
type CommitResult struct {
	Report      commands.CompletionReport
	Positions   []uint64
	Constraints []ConstraintViolation
	Concurrency []ConcurrencyViolation
	Errors      []error
}

// ConstraintViolation preserves provider diagnostics without exposing raw Details
// as HTTP validation state. Property names are mapped to command wire members.
type ConstraintViolation struct {
	Name, Message, Property, Type string
	Details                       map[string]string
}

// ConcurrencyViolation identifies a rejected target and exact provider positions.
type ConcurrencyViolation struct {
	Source           EventSourceID
	Expected, Actual uint64
}

// EventSourceID is a semantic source identity, distinct from arbitrary UUID/string values.
// Empty means Unspecified, not a request to generate a new identity.
type EventSourceID string
