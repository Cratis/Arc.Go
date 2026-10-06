// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/cratis/arc.go/commands"
)

var (
	ErrInvalid        = errors.New("chronicle integration: invalid configuration or value")
	ErrClosed         = errors.New("chronicle integration: transaction completed")
	ErrConcurrent     = errors.New("chronicle integration: concurrent operation")
	ErrMismatch       = errors.New("chronicle integration: transaction coordinates or actor changed")
	ErrUnsupported    = errors.New("chronicle integration: unsupported capability")
	ErrUnknownOutcome = errors.New("chronicle integration: commit outcome unknown; reconcile before resubmission")
	ErrNotRegistered  = errors.New("chronicle integration: event not registered")
)

// StoreName, Namespace and SequenceID identify distinct routing dimensions.
type StoreName string
type Namespace string
type SequenceID string

// Coordinates fixes one transaction's destination. Empty sequence means EventLog.
type Coordinates struct {
	Store     StoreName
	Namespace Namespace
	Sequence  SequenceID
}

// Route selects source and stream dimensions; empty fields use provider defaults.
type Route struct{ SourceType, StreamType, StreamID string }

// Cause is ordered, trusted audit metadata, not authentication evidence.
type Cause struct {
	Occurred   time.Time
	Type       string
	Properties map[string]string
}

// Actor is audit identity. It never supplies Arc roles or authorization.
type Actor struct{ Subject, Name, UserName string }

// NamedTag retains an exact name/value pair.
type NamedTag struct{ Name, Value string }

// Entry borrows Event only until Stage returns. Providers must snapshot content
// and all metadata before returning; callers must not mutate inputs during Stage.
type Entry struct {
	Source    EventSourceID
	Event     any
	Route     Route
	Occurred  *time.Time
	Subject   *string
	Tags      []string
	NamedTags []NamedTag
	Causation []Cause
}

// ExpectationKind distinguishes a real zero position from absent history.
type ExpectationKind uint8

const (
	Resolve ExpectationKind = iota
	UpperBound
	NoMatchingEvent
	NoCheck
	// ProviderResolved retains an opaque immutable provider expectation, never reread.
	ProviderResolved
)

// Expectation is a tagged check; Token is provider-owned immutable evidence only
// for ProviderResolved. It must never be serialized, fabricated or reused across providers.
type Expectation struct {
	Kind     ExpectationKind
	Position uint64
	Token    any
}

// Filter selects a history. Empty dimensions mean no narrowing.
type Filter struct {
	Source EventSourceID
	Route  Route
	Events []EventType
}

// EventType identifies a persisted event generation.
type EventType struct {
	ID         string
	Generation uint32
}

// LabeledScope protects one history. A source-bound label must equal its source.
type LabeledScope struct {
	Label       string
	Filter      Filter
	Expectation Expectation
}

// Batch preserves entry order across sources, including A1, B1, A2. Empty Entries
// with Scopes executes checks; completely empty batches contain no work.
type Batch struct {
	Entries []Entry
	Scopes  []LabeledScope
}

// Participant stages immutable provider-owned work; it cannot complete it.
type Participant interface {
	Stage(context.Context, Batch) error
}

// CompletionOwner is retained privately by Arc's command root, never in context.
// Commit is single-attempt, with actual disposition even on errors; no retry.
type CompletionOwner interface {
	Commit(context.Context) (CommitResult, error)
	Rollback() error
}

// TransactionFactory binds without staging a second application event buffer.
type TransactionFactory interface {
	Begin(context.Context, Coordinates) (Participant, CompletionOwner, error)
}

// EventDescriptor describes a normalized concrete event without SDK types.
type EventDescriptor struct {
	Type     reflect.Type
	Identity EventType
	Validate func(any) error
	Decode   func([]byte) (any, error)
}

// EventCatalog is immutable and selected for the configured store without I/O.
type EventCatalog interface{ Descriptors() []EventDescriptor }

// ScopeRequest resolves policy for the actual target, never another source's tail.
type ScopeRequest struct {
	Coordinates Coordinates
	Filter      Filter
}
type ScopeResolver interface {
	ResolveScope(context.Context, ScopeRequest) (LabeledScope, error)
}

// RecordedEvent is an ordered materialized history item.
type RecordedEvent struct {
	Event    any
	Type     EventType
	Position uint64
}
type HistoryRequest struct {
	Coordinates Coordinates
	Filter      Filter
}
type History struct {
	Events []RecordedEvent
	Scope  LabeledScope
}
type HistoryReader interface {
	ReadHistory(context.Context, HistoryRequest) (History, error)
}

// StoreResolver must select coordinates without network I/O or identity mutation.
type StoreResolver func(context.Context, commands.CommandContext) (Coordinates, error)
