// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/validation"
)

var currentIntegration = commands.NewStateKey[*Integration]()

// AggregateRoot is a callback-bound mutation capability. Embed *AggregateRoot in
// an aggregate; re-resolve through its factory in each Provide/Handle stage. Do not
// retain a root capability or use an aggregate concurrently. Domain fields are
// borrowed shared state within the root command, never a protected read token.
type AggregateRoot struct {
	integration *Integration
	invocation  *commands.Invocation
	transaction *transaction
	state       *aggregateState
	source      EventSourceID
	route       Route
	apply       func(any) error
}
type aggregateState struct {
	mu       sync.Mutex
	busy     bool
	isNew    bool
	findings []validation.Result
	value    any
}

func (s *aggregateState) enter() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrConcurrent
	}
	s.busy = true
	return nil
}
func (s *aggregateState) leave() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }
func (a *AggregateRoot) check(ctx context.Context) error {
	if a == nil || a.invocation == nil {
		return ErrInvalid
	}
	execution := a.invocation.Execution()
	if execution == nil {
		return commands.ErrExecutionClosed
	}
	return execution.Check(ctx)
}

// IsNew reports whether the loaded history was empty; local Apply does not change it.
func (a *AggregateRoot) IsNew() bool { return a != nil && a.state != nil && a.state.isNew }

// Apply snapshots/stages before applying local state, matching C#. A failed local
// handler poisons the shared transaction even if its caller ignores the error.
func (a *AggregateRoot) Apply(ctx context.Context, event any) (err error) {
	if err = a.check(ctx); err != nil {
		return err
	}
	if err = a.state.enter(); err != nil {
		return err
	}
	defer a.state.leave()
	defer func() {
		if recovered := recover(); recovered != nil {
			a.transaction.poison(ErrInvalid)
			panic(recovered)
		}
		a.transaction.poison(err)
	}()
	if err = a.integration.stage(ctx, a.invocation, Batch{Entries: []Entry{{Source: a.source, Route: a.route, Event: event}}}); err != nil {
		return err
	}
	return a.apply(event)
}

// Failed records diagnostics. Error severity poisons automatic commitment. Warning
// and Information do not block persistence unless returned as command diagnostics.
func (a *AggregateRoot) Failed(message string, severity validation.Severity) error {
	if a == nil || a.invocation == nil || a.invocation.Execution() == nil {
		return commands.ErrExecutionClosed
	}
	if severity < validation.Unknown || severity > validation.Error {
		a.transaction.poison(validation.ErrInvalidSeverity)
		return validation.ErrInvalidSeverity
	}
	finding := validation.Result{Message: message, Severity: severity}
	a.state.mu.Lock()
	a.state.findings = append(a.state.findings, finding)
	a.state.mu.Unlock()
	if severity == validation.Error {
		a.transaction.poison(validation.Reject(finding))
	}
	return nil
}

// AggregateCommitResult describes the entire shared owner, not just this aggregate.
// Positions must not be attributed to a single aggregate in a mixed batch.
type AggregateCommitResult struct {
	CommitResult
	Findings []validation.Result
}

// Commit is explicit early finalization of the shared owner. No successor is ever
// created; subsequent Apply or Commit fails. Automatic root completion retains it.
func (a *AggregateRoot) Commit(ctx context.Context) (AggregateCommitResult, error) {
	if err := a.check(ctx); err != nil {
		return AggregateCommitResult{}, err
	}
	a.transaction.mu.Lock()
	closed := a.transaction.closed
	a.transaction.mu.Unlock()
	if closed {
		return AggregateCommitResult{}, ErrClosed
	}
	_, err := a.integration.finish(ctx, a.invocation, a.transaction, true)
	a.transaction.mu.Lock()
	result := a.transaction.result
	a.transaction.mu.Unlock()
	a.state.mu.Lock()
	findings := slices.Clone(a.state.findings)
	a.state.mu.Unlock()
	err = errors.Join(err, commands.ReportCommit(ctx, a.invocation, result.Report))
	return AggregateCommitResult{CommitResult: result, Findings: findings}, err
}

// AggregateOption compiles typed handlers and routing without invoking application code.
type AggregateOption[A any] func(*aggregateDefinition[A]) error
type aggregateDefinition[A any] struct {
	handlers map[reflect.Type]func(A, any) error
	route    Route
	activate func(context.Context, A) error
}

// AggregateFactory owns a frozen constructor and typed dispatch plan, not aggregate instances.
type AggregateFactory[A any] struct {
	definition aggregateDefinition[A]
	construct  func(*AggregateRoot) A
	rootField  int
}

// OnAggregateEvent adds one exact non-pointer event-struct handler. Apply accepts
// values or pointers to that event. Duplicate event claims fail.
func OnAggregateEvent[A, E any](handle func(A, E) error) AggregateOption[A] {
	return func(d *aggregateDefinition[A]) error {
		if handle == nil {
			return ErrInvalid
		}
		typ := reflect.TypeFor[E]()
		if typ.Kind() != reflect.Struct {
			return ErrInvalid
		}
		if _, found := d.handlers[typ]; found {
			return commands.ErrDuplicate
		}
		d.handlers[typ] = func(aggregate A, event any) error {
			if value, ok := event.(E); ok {
				return handle(aggregate, value)
			}
			value := reflect.ValueOf(event)
			if value.Kind() == reflect.Pointer && !value.IsNil() {
				if typed, ok := value.Elem().Interface().(E); ok {
					return handle(aggregate, typed)
				}
			}
			return ErrInvalid
		}
		return nil
	}
}

// WithAggregateRoute overrides defaults. StreamType defaults to the aggregate's simple type name.
func WithAggregateRoute[A any](route Route) AggregateOption[A] {
	return func(d *aggregateDefinition[A]) error { d.route = route; return nil }
}

// WithAggregateActivation runs only after successful rehydration and scope enrollment.
func WithAggregateActivation[A any](activate func(context.Context, A) error) AggregateOption[A] {
	return func(d *aggregateDefinition[A]) error {
		if activate == nil {
			return ErrInvalid
		}
		d.activate = activate
		return nil
	}
}

// DefineAggregate accepts a pointer to a struct directly embedding *AggregateRoot.
// Handlers are typed callbacks, never method-name reflection. Historical handlers
// must only fold state: the root refuses mutations while rehydration is in progress.
func DefineAggregate[A any](construct func(*AggregateRoot) A, options ...AggregateOption[A]) (*AggregateFactory[A], error) {
	typ := reflect.TypeFor[A]()
	if construct == nil || typ.Kind() != reflect.Pointer || typ.Elem().Kind() != reflect.Struct {
		return nil, ErrInvalid
	}
	field, indexFound := typ.Elem().FieldByName("AggregateRoot")
	if !indexFound || len(field.Index) != 1 || field.Type != reflect.TypeFor[*AggregateRoot]() || !field.Anonymous {
		return nil, ErrInvalid
	}
	definition := aggregateDefinition[A]{handlers: map[reflect.Type]func(A, any) error{}, route: Route{StreamType: typ.Elem().Name()}}
	for _, option := range options {
		if option == nil {
			return nil, ErrInvalid
		}
		if err := option(&definition); err != nil {
			return nil, err
		}
	}
	if definition.route.StreamType == "" {
		definition.route.StreamType = typ.Elem().Name()
	}
	return &AggregateFactory[A]{definition: definition, construct: construct, rootField: field.Index[0]}, nil
}

type aggregateKey struct {
	factory     any
	source      EventSourceID
	coordinates Coordinates
	route       Route
}

// Get loads matching history once per root/factory/source/route and refreshes its
// callback capability. Loaded expectations are enrolled without a later tail read.
func (f *AggregateFactory[A]) Get(ctx context.Context, inv *commands.Invocation) (A, error) {
	var zero A
	integration, found, err := commands.FrameState(ctx, inv, currentIntegration)
	if err != nil {
		return zero, err
	}
	if !found || f == nil {
		return zero, ErrInvalid
	}
	tx, err := integration.transaction(ctx, inv)
	if err != nil {
		return zero, err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			tx.poison(ErrInvalid)
			panic(recovered)
		}
	}()
	frame, err := integration.frameFor(ctx, inv)
	if err != nil {
		return zero, err
	}
	if frame.source == "" || isNil(integration.options.History) {
		return zero, ErrInvalid
	}
	key := aggregateKey{factory: f, source: frame.source, coordinates: frame.coordinates, route: f.definition.route}
	tx.mu.Lock()
	if tx.closed || (tx.owner != nil && (tx.coordinates != frame.coordinates || tx.actor != frame.actor)) {
		tx.mu.Unlock()
		return zero, ErrMismatch
	}
	existing, found := tx.aggregates[key]
	if !found {
		tx.aggregates[key] = (*aggregateState)(nil)
	}
	tx.mu.Unlock()
	var state *aggregateState
	if found {
		state = existing.(*aggregateState)
		if state == nil {
			return zero, ErrConcurrent
		}
	} else {
		state = &aggregateState{busy: true}
		history, err := integration.options.History.ReadHistory(frame.context(ctx), HistoryRequest{Coordinates: frame.coordinates, Filter: Filter{Source: frame.source, Route: f.definition.route}})
		if err != nil {
			tx.poison(err)
			return zero, err
		}
		state.isNew = len(history.Events) == 0
		aggregate := f.construct(&AggregateRoot{state: state}) // deliberately no mutation capability during folding
		if isNil(aggregate) {
			tx.poison(ErrInvalid)
			return zero, ErrInvalid
		}
		for index, event := range history.Events {
			descriptor, registered := integration.descriptor(event.Event)
			if !registered || descriptor.Identity != event.Type || (index > 0 && event.Position <= history.Events[index-1].Position) {
				tx.poison(ErrUnsupported)
				return zero, ErrUnsupported
			}
			handler, ok := f.definition.handlers[reflect.TypeOf(event.Event)]
			if !ok {
				tx.poison(ErrUnsupported)
				return zero, ErrUnsupported
			}
			if err := handler(aggregate, event.Event); err != nil {
				tx.poison(err)
				return zero, err
			}
		}
		state.value = aggregate
		if err := integration.stage(ctx, inv, Batch{Scopes: []LabeledScope{history.Scope}}); err != nil {
			return zero, err
		}
		state.busy = false
		tx.mu.Lock()
		tx.aggregates[key] = state
		tx.mu.Unlock()
	}
	if err := state.enter(); err != nil {
		return zero, err
	}
	aggregate := state.value.(A)
	root := &AggregateRoot{integration: integration, invocation: inv, transaction: tx, state: state, source: frame.source, route: f.definition.route}
	root.apply = func(event any) error {
		typ := reflect.TypeOf(event)
		if typ != nil && typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		handler, found := f.definition.handlers[typ]
		if !found {
			return ErrUnsupported
		}
		return handler(aggregate, event)
	}
	reflect.ValueOf(aggregate).Elem().Field(f.rootField).Set(reflect.ValueOf(root))
	state.leave()
	if !found && f.definition.activate != nil {
		if err := f.definition.activate(ctx, aggregate); err != nil {
			tx.poison(err)
			return zero, err
		}
	}
	return aggregate, nil
}
