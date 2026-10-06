// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"

	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/observability"
)

func (p *queryPipeline) Diagnostics() *observability.Recorder { return p.options.Diagnostics }

func beginDiagnostics(ctx context.Context, p Pipeline, name FullyQualifiedQueryName, transport observability.Transport, phase observability.Phase) (context.Context, *boundary.Attempt) {
	capability, ok := p.(boundary.DiagnosticSource)
	if !ok || capability.Diagnostics() == nil {
		return ctx, nil
	}
	if _, known := p.Lookup(name); !known {
		name = ""
	}
	return boundary.Begin(ctx, p, observability.Query, string(name), transport, phase)
}

func finishDiagnostics[R any](attempt *boundary.Attempt, ctx context.Context, result Result[R], err error) {
	if attempt == nil {
		return
	}
	attempt.Finish(boundary.Outcome(result.IsAuthorized(), result.HasExceptions(), err != nil, ctx != nil && ctx.Err() != nil, result.details.ValidationResults))
}
