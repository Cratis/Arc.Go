// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"

	"github.com/cratis/arc.go/commands"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/queries"
)

func beginCommandDiagnostics(ctx context.Context, p admittedCommands, command any, validate bool) (context.Context, *boundary.Attempt) {
	if p.Diagnostics() == nil {
		return ctx, nil
	}
	registration, _ := p.LookupCommand(command)
	operation := observability.Command
	if validate {
		operation = observability.Validate
	}
	return boundary.Begin(ctx, p, operation, registration.Descriptor().Type.Identity(), observability.Unknown, observability.Completed)
}
func beginQueryDiagnostics(ctx context.Context, p admittedQueries, name queries.FullyQualifiedQueryName, transport observability.Transport, phase observability.Phase) (context.Context, *boundary.Attempt) {
	if p.Diagnostics() == nil {
		return ctx, nil
	}
	if _, known := p.Lookup(name); !known {
		name = ""
	}
	return boundary.Begin(ctx, p, observability.Query, string(name), transport, phase)
}
func finishCommandDiagnostics[R any](attempt *boundary.Attempt, ctx context.Context, result commands.Result[R], err error) {
	if attempt == nil {
		return
	}
	attempt.Finish(boundary.Outcome(result.IsAuthorized(), result.HasExceptions(), err != nil, ctx != nil && ctx.Err() != nil, result.Details().ValidationResults))
}
func finishQueryDiagnostics[R any](attempt *boundary.Attempt, ctx context.Context, result queries.Result[R], err error) {
	if attempt == nil {
		return
	}
	attempt.Finish(boundary.Outcome(result.IsAuthorized(), result.HasExceptions(), err != nil, ctx != nil && ctx.Err() != nil, result.Details().ValidationResults))
}
