// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"time"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

// QueryContext is an immutable metadata snapshot, not a provider or authorization token.
// Raw byte/string slices are copied; other argument objects remain borrowed.
type QueryContext struct {
	name          FullyQualifiedQueryName
	correlationID correlation.ID
	receivedAt    time.Time
	principal     identity.Principal
	tenant        tenancy.ID
	arguments     Arguments
	parameters    Parameters
	totalItems    int64
}

// Name returns the logical query identity.
func (c QueryContext) Name() FullyQualifiedQueryName { return c.name }

// CorrelationID returns the operation correlation.
func (c QueryContext) CorrelationID() correlation.ID { return c.correlationID }

// ReceivedAt returns the UTC receipt timestamp.
func (c QueryContext) ReceivedAt() time.Time { return c.receivedAt }

// Principal returns an immutable security snapshot.
func (c QueryContext) Principal() identity.Principal { return c.principal }

// Tenant returns the selected tenant, not a membership verdict.
func (c QueryContext) Tenant() tenancy.ID { return c.tenant }

// Arguments returns raw input presence and provenance.
func (c QueryContext) Arguments() Arguments { return c.arguments }

// Parameters returns request controls by value.
func (c QueryContext) Parameters() Parameters { return c.parameters }

// TotalItems returns the renderer/page total, zero before rendering.
func (c QueryContext) TotalItems() int64 { return c.totalItems }

type queryContextKey struct{}

// ContextFrom reads query metadata. The value contains no scope/execution owner.
func ContextFrom(ctx context.Context) (QueryContext, bool) {
	if ctx == nil {
		return QueryContext{}, false
	}
	c, ok := ctx.Value(queryContextKey{}).(QueryContext)
	return c, ok
}

// Invocation is a callback-scoped frame. Its scope expires after that callback,
// cannot close operation resources, and must not be retained or used asynchronously.
type Invocation struct {
	queryContext QueryContext
	scope        *execution.Scope
}

// QueryContext returns callback metadata without granting authority.
func (i *Invocation) QueryContext() QueryContext {
	if i == nil {
		return QueryContext{}
	}
	return i.queryContext
}

// Scope returns the expiring non-closing operation view; nil invocation yields nil.
func (i *Invocation) Scope() *execution.Scope {
	if i == nil {
		return nil
	}
	return i.scope
}
