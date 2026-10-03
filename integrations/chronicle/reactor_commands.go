// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"sync"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

// Delivery is trusted observer metadata, not proof of a user's authority.
// ID is an idempotency input; Arc does not provide exactly-once execution.
type Delivery struct {
	ID          string
	Coordinates Coordinates
	Correlation correlation.ID
	Causes      []Cause
	Replay      bool
}

// ReplayPolicy requires an explicit automation choice. LiveOnly is recommended;
// replay exclusion is not deduplication during ordinary failure recovery.
type ReplayPolicy uint8

const (
	LiveOnly ReplayPolicy = iota + 1
	IncludeReplay
)

// ReactorCommandOptions fixes trusted service identity and coordinate mappings.
// Principal is required, including an explicit anonymous value when appropriate.
type ReactorCommandOptions struct {
	Store     StoreName
	Principal *identity.Principal
	Replay    ReplayPolicy
	Tenant    func(Namespace) (tenancy.ID, error)
}

// ReactorCommands is concurrent-safe. Construct before client compilation, then
// Bind the built app before registering/starting observers. It never retains a
// current operation scope; each command enters a fresh root Arc operation.
type ReactorCommands struct {
	mu        sync.RWMutex
	options   ReactorCommandOptions
	principal identity.Principal
	pipeline  commands.Pipeline
}

func NewReactorCommands(options ReactorCommandOptions) (*ReactorCommands, error) {
	if options.Store == "" || options.Principal == nil || (options.Replay != LiveOnly && options.Replay != IncludeReplay) {
		return nil, ErrInvalid
	}
	return &ReactorCommands{options: options, principal: *options.Principal}, nil
}

// Bind is one-time wiring. Start Arc admission before observers can deliver work.
func (r *ReactorCommands) Bind(app *arc.Application) error {
	if r == nil || app == nil {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pipeline != nil {
		return commands.ErrFrozen
	}
	r.pipeline = app.Commands()
	return nil
}

// CommandFailure preserves a failed result, error and delivery ID for diagnostics.
type CommandFailure struct {
	DeliveryID string
	Result     commands.Result[any]
	Cause      error
}

func (e *CommandFailure) Error() string { return "chronicle reactor command failed" }
func (e *CommandFailure) Unwrap() error { return e.Cause }

// Execute preflights command registration, then executes in order before observer
// acknowledgement. It stops on the first failed error OR envelope; earlier
// commits remain committed and may repeat if the kernel redelivers. No local retry.
func (r *ReactorCommands) Execute(ctx context.Context, delivery Delivery, values ...any) error {
	if r == nil || ctx == nil || delivery.Coordinates.Store != r.options.Store || delivery.Coordinates.Namespace == "" || delivery.Correlation == (correlation.ID{}) || delivery.ID == "" {
		return ErrMismatch
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if delivery.Replay && r.options.Replay == LiveOnly {
		return nil
	}
	tenant, err := tenancy.ParseID(string(delivery.Coordinates.Namespace))
	if err != nil {
		return err
	}
	if r.options.Tenant != nil {
		tenant, err = r.options.Tenant(delivery.Coordinates.Namespace)
		if err != nil {
			return err
		}
	}
	r.mu.RLock()
	pipeline := r.pipeline
	r.mu.RUnlock()
	if pipeline == nil {
		return commands.ErrNoContext
	}
	for _, value := range values {
		if _, err := pipeline.LookupCommand(value); err != nil {
			return err
		}
	}
	ctx = identity.WithPrincipal(ctx, r.principal)
	ctx = tenancy.WithTenant(ctx, tenant)
	ctx = correlation.WithID(ctx, delivery.Correlation)
	expected := delivery.Coordinates
	expected.Sequence = ""
	ctx = WithMetadata(ctx, Metadata{Causes: delivery.Causes, Expected: &expected})
	for _, value := range values {
		result, err := pipeline.Execute(ctx, value)
		if err != nil || !result.IsSuccess() {
			return &CommandFailure{DeliveryID: delivery.ID, Result: result, Cause: err}
		}
	}
	return nil
}
