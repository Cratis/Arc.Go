// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"sync"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
)

// Options borrows concurrent-safe collaborators. Construction/Install does no I/O.
// Audit is explicitly trusted, non-PII scalar metadata; nil audits no input values.
// Actor defaults to authenticated subject/display name, never claims or usernames.
type Options struct {
	StoreResolver StoreResolver
	Transactions  TransactionFactory
	Events        EventCatalog
	History       HistoryReader
	Models        ModelReader
	Appends       AppendObserver
	Concurrency   ScopeResolver
	Actor         func(identity.Principal) Actor
	Audit         func(commands.CommandContext) map[string]string
	// SemanticSource optionally recognizes a provider's exact semantic identity type.
	SemanticSource func(any) (EventSourceID, bool)
	Logger         *slog.Logger
}

// Integration is single-owner during configuration and immutable after Install.
// It owns no client or event payload buffer. Discard a builder after failed Install.
type Integration struct {
	options        Options
	events         map[reflect.Type]EventDescriptor
	configurations map[reflect.Type]CommandOptions
	root           commands.StateKey[*transaction]
	frame          commands.StateKey[*commandFrame]
	bindings       []func(*commands.Registry) error
	installed      bool
}

// New validates immutable catalog membership without network I/O.
func New(options Options) (*Integration, error) {
	if options.StoreResolver == nil || isNil(options.Transactions) || isNil(options.Events) {
		return nil, ErrInvalid
	}
	i := &Integration{options: options, events: map[reflect.Type]EventDescriptor{}, configurations: map[reflect.Type]CommandOptions{}, root: commands.NewStateKey[*transaction](), frame: commands.NewStateKey[*commandFrame]()}
	for _, d := range options.Events.Descriptors() {
		if d.Type == nil || d.Type.Kind() != reflect.Struct || d.Identity.ID == "" || d.Identity.Generation == 0 || d.Validate == nil {
			return nil, ErrInvalid
		}
		if _, found := i.events[d.Type]; found {
			return nil, commands.ErrDuplicate
		}
		i.events[d.Type] = d
	}
	return i, nil
}

// Install adds state, source resolution, returned-event consumers and one terminal
// participant. No Chronicle connection or application constructor is activated.
func (i *Integration) Install(builder *arc.Builder) error {
	if i == nil || builder == nil {
		return ErrInvalid
	}
	if i.installed {
		return commands.ErrFrozen
	}
	i.installed = true
	registry := builder.Commands()
	if err := registry.AddDeferredCommitParticipant("chronicle", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) { return i, nil }); err != nil {
		return err
	}
	if err := registry.AddContextValuesProvider("chronicle", func(context.Context, *execution.Scope) (commands.ContextValuesProvider, error) { return i, nil }); err != nil {
		return err
	}
	types := []reflect.Type{reflect.TypeFor[EventBatch](), reflect.TypeFor[EventValue](), reflect.TypeFor[AggregateCommitResult]()}
	for typ := range i.events {
		types = append(types, typ, reflect.PointerTo(typ))
	}
	if err := registry.AddReturnAdmission("chronicle", commands.ReturnAdmission{Types: types, Check: i.admit}); err != nil {
		return err
	}
	if err := registry.AddResponseValueHandler("chronicle", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) { return i, nil }); err != nil {
		return err
	}
	if !isNil(i.options.Appends) {
		if err := registry.AddFilter("chronicle.appends", func(context.Context, *execution.Scope) (commands.Filter, error) { return i, nil }); err != nil {
			return err
		}
	}
	for _, bind := range i.bindings {
		if err := bind(registry); err != nil {
			return err
		}
	}
	return nil
}

// CommandOptions supplies explicit source/route/compliance and concurrency metadata.
// Empty Source selects semantic conventions; empty Sequence selects event-log.
// A concurrency dimension is included only when its corresponding flag is true.
type CommandOptions struct {
	Source                                                            func(any) EventSourceID
	Sequence                                                          SequenceID
	Route                                                             Route
	Subject                                                           func(any) *string
	ConcurrencySourceType, ConcurrencyStreamType, ConcurrencyStreamID bool
}

// ConfigureCommand freezes per-command routing before Install. Duplicate declarations fail.
func ConfigureCommand[C any](i *Integration, options CommandOptions) error {
	if i == nil {
		return ErrInvalid
	}
	if i.installed {
		return commands.ErrFrozen
	}
	typ := reflect.TypeFor[C]()
	if _, ok := i.configurations[typ]; ok {
		return commands.ErrDuplicate
	}
	i.configurations[typ] = options
	return nil
}

type commandFrame struct {
	coordinates Coordinates
	source      EventSourceID
	options     CommandOptions
	actor       Actor
	causes      []Cause
	scopes      map[string]LabeledScope
	modelMu     sync.Mutex
	models      map[modelCacheKey]ModelDocument
	modelBusy   map[modelCacheKey]bool
}

func (i *Integration) frameFor(ctx context.Context, inv *commands.Invocation) (*commandFrame, error) {
	value, found, err := commands.FrameState(ctx, inv, i.frame)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, commands.ErrNoContext
	}
	return value, nil
}

// CoordinatesFor returns the command's frozen routing after checking admission.
// Namespace selection is not an authorization or membership decision.
func (i *Integration) CoordinatesFor(ctx context.Context, inv *commands.Invocation) (Coordinates, error) {
	frame, err := i.frameFor(ctx, inv)
	if err != nil {
		return Coordinates{}, err
	}
	return frame.coordinates, nil
}
func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func:
		return v.IsNil()
	}
	return false
}
func validCoordinates(c Coordinates) bool {
	return strings.TrimSpace(string(c.Store)) != "" && strings.TrimSpace(string(c.Namespace)) != "" && strings.TrimSpace(string(c.Sequence)) != ""
}
