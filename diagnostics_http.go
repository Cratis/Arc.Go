// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"net/http"

	"github.com/cratis/arc.go/commands"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/internal/websockettransport"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/queries"
)

type httpDiagnosticsKey struct{}
type httpDiagnostics struct {
	attempt *boundary.Attempt
	outcome observability.Outcome
}

// Only matched backend endpoints have attempts. Authentication and reader callbacks
// never see the dispatch token; nested backend calls therefore own fresh attempts.
func (a *Application) beginHTTPDiagnostics(r *http.Request) (*http.Request, *httpDiagnostics) {
	if a.options.Diagnostics == nil || r.URL == nil {
		return r, nil
	}
	e, matched := a.routeTable[r.URL.Path][r.Method]
	if !matched {
		return r, nil
	}
	operation, transport, phase := observability.Command, observability.Unknown, observability.Completed
	var source any = admittedCommands{a}
	if e.Method == "POST" {
		if _, known := a.commands.Lookup(e.Identity); !known {
			return r, nil
		}
		if e.ValidateOnly {
			operation = observability.Validate
		}
	} else {
		q, known := a.queries.Lookup(queries.FullyQualifiedQueryName(e.Identity))
		if !known {
			return r, nil
		}
		source, operation, transport = admittedQueries{a}, observability.Query, observability.SnapshotTransport
		if r.Method != "HEAD" && q.Descriptor().Observable && (acceptsSSE(r) || websockettransport.IsUpgrade(r)) {
			transport, phase = observability.ObservableTransport, observability.Opening
		}
	}
	_, attempt := boundary.Begin(r.Context(), source, operation, e.Identity, transport, phase)
	diagnostic := &httpDiagnostics{attempt: attempt, outcome: observability.Error}
	return r.WithContext(context.WithValue(r.Context(), httpDiagnosticsKey{}, diagnostic)), diagnostic
}

func (d *httpDiagnostics) finish(ctx context.Context) {
	if d == nil {
		return
	}
	outcome := d.outcome
	if outcome == observability.Error && ctx.Err() != nil {
		outcome = observability.Cancelled
	}
	d.attempt.FinishForwarded(outcome)
}

func httpDiagnosticResult(ctx context.Context, value any) {
	d, _ := ctx.Value(httpDiagnosticsKey{}).(*httpDiagnostics)
	if d == nil {
		return
	}
	switch result := value.(type) {
	case commands.Result[any]:
		d.outcome = boundary.Outcome(result.IsAuthorized(), result.HasExceptions(), false, ctx.Err() != nil, result.Details().ValidationResults)
	case commands.Result[commands.NoResponse]:
		d.outcome = boundary.Outcome(result.IsAuthorized(), result.HasExceptions(), false, ctx.Err() != nil, result.Details().ValidationResults)
	case queries.Result[any]:
		d.outcome = boundary.Outcome(result.IsAuthorized(), result.HasExceptions(), false, ctx.Err() != nil, result.Details().ValidationResults)
	}
}
