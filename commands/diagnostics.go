// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"

	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/observability"
)

func (p *pipeline) Diagnostics() *observability.Recorder      { return p.options.Diagnostics }
func (b *boundPipeline) Diagnostics() *observability.Recorder { return b.pipeline.Diagnostics() }

func beginDiagnostics(ctx context.Context, p Pipeline, command any, validate bool) (context.Context, *boundary.Attempt) {
	capability, ok := p.(boundary.DiagnosticSource)
	if !ok || capability.Diagnostics() == nil {
		return ctx, nil
	}
	registration, _ := p.LookupCommand(command)
	operation := observability.Command
	if validate {
		operation = observability.Validate
	}
	return boundary.Begin(ctx, p, operation, registration.descriptor.Type.Identity(), observability.Unknown, observability.Completed)
}

func finishDiagnostics[R any](attempt *boundary.Attempt, ctx context.Context, result Result[R], err error) {
	if attempt == nil {
		return
	}
	attempt.Finish(boundary.Outcome(result.IsAuthorized(), result.HasExceptions(), err != nil, ctx != nil && ctx.Err() != nil, result.details.ValidationResults))
}
