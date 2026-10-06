// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package generatedconsumer is an executable model-bound authoring fixture.
//
//arc:namespace Generated.Shop
package generatedconsumer

import (
	"context"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/validation"
)

// Reader loads preparation state.
type Reader interface {
	Load(context.Context, string) (State, error)
}

// Writer performs accepted work.
type Writer interface {
	Write(context.Context, State) (string, error)
}

// ItemQueries supplies snapshot data.
type ItemQueries interface {
	All(context.Context, string) ([]Item, error)
}

// State is a single explicitly typed preparation payload.
type State struct{ Name string }

// AddItem prepares state before resolving the writer.
//
//arc:command block-on=warning
//arc:authorize roles=Editor,Admin
//arc:authorize roles=Verified
//arc:exclude-from-discovery
type AddItem struct {
	Name string `json:"name" validate:"required"`
	Stop bool   `json:"stop"`
}

// Validate is invoked by the same runtime convention as manual registration.
func (c AddItem) Validate(context.Context) ([]validation.Result, error) {
	if c.Name == "invalid" {
		return []validation.Result{{Severity: validation.Error, Message: "Invalid name", Members: []string{"name"}}}, nil
	}
	return nil, nil
}

// Provide resolves only the reader and can stop successfully before handling.
func (c AddItem) Provide(ctx context.Context, reader Reader, info commands.CommandContext) (commands.Preparation[State], error) {
	if c.Stop {
		return commands.StopProviding[State](commands.Success(info.CorrelationID())), nil
	}
	state, err := reader.Load(ctx, c.Name)
	if err != nil {
		return commands.Preparation[State]{}, err
	}
	return commands.Provided(state), nil
}

// Handle receives the exact prepared value; Writer is not resolved earlier.
func (AddItem) Handle(ctx context.Context, state State, writer Writer) (string, error) {
	return writer.Write(ctx, state)
}

// Rename uses an ordinary typed Provide result.
//
//arc:command
//arc:allow-anonymous
type Rename struct {
	Name string `json:"name"`
}

// Provide supplies a typed value without a container.
func (c Rename) Provide(context.Context) (State, error) { return State(c), nil }

// Handle consumes the prepared value.
func (Rename) Handle(_ context.Context, state State) (string, error) { return state.Name, nil }

// Reset demonstrates a pointer command and optional context.
//
//arc:command
//arc:allow-anonymous
type Reset struct{}

// Handle performs a void command.
func (*Reset) Handle() error { return nil }

// Checked demonstrates validation-only preparation controls.
//
//arc:command
//arc:allow-anonymous
type Checked struct {
	Warning bool `json:"warning"`
}

// Provide uses the wider preparation control grammar.
func (c Checked) Provide() ([]validation.Result, error) {
	severity := validation.Error
	if c.Warning {
		severity = validation.Warning
	}
	return []validation.Result{{Severity: severity, Message: "Preparation finding"}}, nil
}

// Handle runs only after preparation is accepted.
func (Checked) Handle(ctx context.Context, writer Writer) (string, error) {
	return writer.Write(ctx, State{Name: "checked"})
}

// Guarded demonstrates authorization-only preparation controls.
//
//arc:command
//arc:allow-anonymous
type Guarded struct{}

// Provide denies before the writer can be resolved.
func (Guarded) Provide(context.Context) (authorization.Decision, error) {
	return authorization.Deny("Not allowed"), nil
}

// Handle must not run when Provide denies.
func (Guarded) Handle(ctx context.Context, writer Writer) (string, error) {
	return writer.Write(ctx, State{})
}

// Item owns two generated queries.
//
//arc:readmodel
//arc:authorize roles=Reader
type Item struct {
	ID   string `json:"id" arc:"identity"`
	Name string `json:"name"`
}

// Arguments is caller input, never reclassified as a dependency.
type Arguments struct {
	Prefix string `json:"prefix" query:"required"`
}

// AllItems is the primary static-equivalent authoring shape.
func (Item) AllItems(ctx context.Context, args Arguments, items ItemQueries, info queries.QueryContext, parameters queries.Parameters) ([]Item, error) {
	if info.Name() == "" || parameters.Paging.IsPaged {
		return nil, nil
	}
	return items.All(ctx, args.Prefix)
}

// RecentItems is the explicit function alternative with replacement authorization.
//
//arc:query model=Item name=Recent path=/generated/recent http=QUERY
//arc:allow-anonymous
func RecentItems(ctx context.Context, args Arguments, items ItemQueries) ([]Item, error) {
	return items.All(ctx, args.Prefix)
}

// Label is unrelated behavior, not an implicitly discovered query.
func (Item) Label() string { return "ordinary method" }

//arc:ignore
func (Item) Ignored(context.Context) ([]Item, error) { return nil, nil }
