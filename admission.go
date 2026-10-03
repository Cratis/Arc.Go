// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/queries"
)

type admittedCommands struct{ a *Application }

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
func (p admittedCommands) run(ctx context.Context, s *execution.Scope, c any, o []commands.ExecuteOptions, scoped bool) (commands.Result[any], error) {
	work, release, err := p.a.admit(ctx)
	if err != nil {
		return commands.FromError[any](contextID(ctx), err), err
	}
	defer release()
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
func (p admittedCommands) validate(ctx context.Context, s *execution.Scope, c any, o []commands.ExecuteOptions, scoped bool) (commands.Result[commands.NoResponse], error) {
	work, release, err := p.a.admit(ctx)
	if err != nil {
		return commands.FromError[commands.NoResponse](contextID(ctx), err), err
	}
	defer release()
	if scoped {
		return p.a.commands.ValidateScoped(work, s, c, o...)
	}
	return p.a.commands.Validate(work, c, o...)
}

type admittedQueries struct{ a *Application }

func (p admittedQueries) Lookup(name queries.FullyQualifiedQueryName) (queries.Registration, bool) {
	return p.a.queries.Lookup(name)
}
func (p admittedQueries) Perform(ctx context.Context, name queries.FullyQualifiedQueryName, r queries.Request) (queries.Result[any], error) {
	return p.run(ctx, nil, name, r, false)
}
func (p admittedQueries) PerformScoped(ctx context.Context, s *execution.Scope, name queries.FullyQualifiedQueryName, r queries.Request) (queries.Result[any], error) {
	return p.run(ctx, s, name, r, true)
}
func (p admittedQueries) run(ctx context.Context, s *execution.Scope, name queries.FullyQualifiedQueryName, r queries.Request, scoped bool) (queries.Result[any], error) {
	work, release, err := p.a.admit(ctx)
	if err != nil {
		return queries.FromError[any](contextID(ctx), err), err
	}
	defer release()
	if scoped {
		return p.a.queries.PerformScoped(work, s, name, r)
	}
	return p.a.queries.Perform(work, name, r)
}
func contextID(ctx context.Context) correlation.ID {
	if ctx == nil {
		return correlation.ID{}
	}
	return correlation.FromContext(ctx)
}
