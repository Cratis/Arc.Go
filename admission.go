// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/queries"
)

type admittedCommands struct{ a *Application }

func (p admittedCommands) Diagnostics() *observability.Recorder { return p.a.options.Diagnostics }

func (p admittedCommands) Lookup(name string) (commands.Registration, bool) {
	return p.a.commands.Lookup(name)
}
func (p admittedCommands) LookupCommand(c any) (commands.Registration, error) {
	return p.a.commands.LookupCommand(c)
}
func (p admittedCommands) Execute(ctx context.Context, c any, o ...commands.ExecuteOptions) (commands.Result[any], error) {
	return p.run(ctx, nil, c, o, false)
}
func (p admittedCommands) ExecuteScoped(ctx context.Context, s *execution.Scope, c any, o ...commands.ExecuteOptions) (commands.Result[any], error) {
	return p.run(ctx, s, c, o, true)
}
func (p admittedCommands) run(ctx context.Context, s *execution.Scope, c any, o []commands.ExecuteOptions, scoped bool) (result commands.Result[any], resultErr error) {
	ctx, attempt := beginCommandDiagnostics(ctx, p, c, false)
	var release func()
	defer func() {
		finishCommandDiagnostics(attempt, ctx, result, resultErr)
		if release != nil {
			release()
		}
	}()
	work, release, err := p.a.admit(ctx)
	if err != nil {
		return commands.FromError[any](contextID(ctx), err), err
	}
	ctx = work
	if scoped {
		return p.a.commands.ExecuteScoped(work, s, c, o...)
	}
	return p.a.commands.Execute(work, c, o...)
}
func (p admittedCommands) Validate(ctx context.Context, c any, o ...commands.ExecuteOptions) (commands.Result[commands.NoResponse], error) {
	return p.validate(ctx, nil, c, o, false)
}
func (p admittedCommands) ValidateScoped(ctx context.Context, s *execution.Scope, c any, o ...commands.ExecuteOptions) (commands.Result[commands.NoResponse], error) {
	return p.validate(ctx, s, c, o, true)
}
func (p admittedCommands) validate(ctx context.Context, s *execution.Scope, c any, o []commands.ExecuteOptions, scoped bool) (result commands.Result[commands.NoResponse], resultErr error) {
	ctx, attempt := beginCommandDiagnostics(ctx, p, c, true)
	var release func()
	defer func() {
		finishCommandDiagnostics(attempt, ctx, result, resultErr)
		if release != nil {
			release()
		}
	}()
	work, release, err := p.a.admit(ctx)
	if err != nil {
		return commands.FromError[commands.NoResponse](contextID(ctx), err), err
	}
	ctx = work
	if scoped {
		return p.a.commands.ValidateScoped(work, s, c, o...)
	}
	return p.a.commands.Validate(work, c, o...)
}

type admittedQueries struct{ a *Application }

func (p admittedQueries) Diagnostics() *observability.Recorder { return p.a.options.Diagnostics }

func (p admittedQueries) Lookup(name queries.FullyQualifiedQueryName) (queries.Registration, bool) {
	return p.a.queries.Lookup(name)
}
func (p admittedQueries) Perform(ctx context.Context, name queries.FullyQualifiedQueryName, r queries.Request) (queries.Result[any], error) {
	return p.run(ctx, nil, name, r, false)
}
func (p admittedQueries) PerformScoped(ctx context.Context, s *execution.Scope, name queries.FullyQualifiedQueryName, r queries.Request) (queries.Result[any], error) {
	return p.run(ctx, s, name, r, true)
}
func (p admittedQueries) run(ctx context.Context, s *execution.Scope, name queries.FullyQualifiedQueryName, r queries.Request, scoped bool) (result queries.Result[any], resultErr error) {
	ctx, attempt := beginQueryDiagnostics(ctx, p, name, observability.SnapshotTransport, observability.Completed)
	var release func()
	defer func() {
		finishQueryDiagnostics(attempt, ctx, result, resultErr)
		if release != nil {
			release()
		}
	}()
	work, release, err := p.a.admit(ctx)
	if err != nil {
		return queries.FromError[any](contextID(ctx), err), err
	}
	ctx = work
	if scoped {
		return p.a.queries.PerformScoped(work, s, name, r)
	}
	return p.a.queries.Perform(work, name, r)
}
func (p admittedQueries) Open(ctx context.Context, name queries.FullyQualifiedQueryName, r queries.Request) (observation *queries.Observation, result queries.Result[any], resultErr error) {
	ctx, attempt := beginQueryDiagnostics(ctx, p, name, observability.ObservableTransport, observability.Opening)
	var releaseUnused func()
	defer func() {
		if attempt != nil {
			attempt.FinishForwarded(boundary.Outcome(result.IsAuthorized(), result.HasExceptions(), resultErr != nil, ctx != nil && ctx.Err() != nil, result.Details().ValidationResults))
		}
		if releaseUnused != nil {
			releaseUnused()
		}
	}()
	// A transport may forward a cleanup-join notification. Compose it with the
	// application lease rather than replacing it; failed opening cleanup can
	// remain retained even when Open returns a nil observation.
	notify := boundary.TakeObservationAdmission(ctx)
	work, release, err := p.a.admit(ctx)
	if err != nil {
		if notify != nil {
			notify()
		}
		return nil, queries.FromError[any](contextID(ctx), err), err
	}
	work, lease := boundary.WithObservationAdmission(work, func() {
		release()
		if notify != nil {
			notify()
		}
	})
	releaseUnused = lease.ReleaseUnused
	capability, ok := p.a.queries.(queries.ObservablePipeline)
	if !ok {
		return nil, queries.FromError[any](contextID(ctx), queries.ErrObservableCapability), queries.ErrObservableCapability
	}
	ctx = work
	return capability.Open(work, name, r)
}
func (p admittedQueries) CloseObservations(ctx context.Context) error {
	capability, ok := p.a.queries.(queries.ObservablePipeline)
	if !ok {
		return queries.ErrObservableCapability
	}
	return capability.CloseObservations(ctx)
}

func contextID(ctx context.Context) correlation.ID {
	if ctx == nil {
		return correlation.ID{}
	}
	return correlation.FromContext(ctx)
}
