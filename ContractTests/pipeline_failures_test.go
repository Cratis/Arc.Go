// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
)

func TestBothPipelinesDenyBeforeApplicationFactories(t *testing.T) {
	for _, mode := range []string{"manual", "resources", "container"} {
		t.Run(mode, func(t *testing.T) {
			f := newPipelineFixture(t, mode, nil)
			ctx := identity.WithPrincipal(pipelineContext(t), identity.System("Reader"))
			command, err := f.commands.Execute(ctx, pipelineCommand{})
			pipelineMust(t, err)
			query, err := f.queries.Perform(ctx, "row.Read", queries.RequestFor(queries.NoArguments{}, queries.Parameters{}))
			pipelineMust(t, err)
			if command.IsAuthorized() || query.IsAuthorized() || f.commandCalls != 0 || f.queryCalls != 0 || f.policyCalls != 0 {
				t.Fatal("denial invoked business or scoped policy factories")
			}
			if mode == "container" && f.opens != 0 {
				t.Fatal("denial constructed a dependency")
			}
			assertPipelinePayloadAbsent(t, pipelineJSON(t, command), "response")
			assertPipelinePayloadAbsent(t, pipelineJSON(t, query), "data")
		})
	}
}

func TestBothPipelinesRejectChangedBorrowedSecurityAndExpiredScopes(t *testing.T) {
	f := newPipelineFixture(t, "manual", nil)
	ctx := pipelineContext(t)
	scope, err := execution.OpenScope(ctx, nil)
	pipelineMust(t, err)
	for _, changed := range []context.Context{
		identity.WithPrincipal(ctx, identity.System("Other")),
		tenancy.WithTenant(ctx, tenancy.ID{}),
	} {
		command, err := f.commands.ExecuteScoped(changed, scope, pipelineCommand{})
		if (!errors.Is(err, execution.ErrIdentityChanged) && !errors.Is(err, tenancy.ErrNotSet)) || command.IsSuccess() {
			t.Fatalf("command accepted changed security: %v", err)
		}
		query, err := f.queries.PerformScoped(changed, scope, "row.Read", queries.RequestFor(queries.NoArguments{}, queries.Parameters{}))
		if (!errors.Is(err, execution.ErrIdentityChanged) && !errors.Is(err, tenancy.ErrNotSet)) || query.IsSuccess() {
			t.Fatalf("query accepted changed security: %v", err)
		}
	}
	pipelineMust(t, scope.Close(ctx))
	if _, err := f.commands.ExecuteScoped(ctx, scope, pipelineCommand{}); !errors.Is(err, execution.ErrScopeClosed) {
		t.Fatal(err)
	}
	if _, err := f.queries.PerformScoped(ctx, scope, "row.Read", queries.RequestFor(queries.NoArguments{}, queries.Parameters{})); !errors.Is(err, execution.ErrScopeClosed) {
		t.Fatal(err)
	}
	if f.commandCalls != 0 || f.queryCalls != 0 {
		t.Fatal("invalid scope invoked business code")
	}
}

func TestNestedQueryDoesNotDisposeAndRootCleanupSuppressesResponse(t *testing.T) {
	cause := errors.New("private cleanup detail")
	for _, mode := range []string{"resources", "container"} {
		t.Run(mode, func(t *testing.T) {
			f := newPipelineFixture(t, mode, cause)
			ctx := pipelineContext(t)
			result, err := f.commands.Execute(ctx, pipelineCommand{})
			if !errors.Is(err, cause) || !result.HasExceptions() || result.IsSuccess() || f.store.closes != 1 || f.queryCalls != 1 {
				t.Fatalf("result = %#v, error = %v, closes = %d", result.Details(), err, f.store.closes)
			}
			if result.Details().CorrelationID != correlation.FromContext(ctx) {
				t.Fatal("cleanup lost correlation")
			}
			assertPipelinePayloadAbsent(t, pipelineJSON(t, result), "response")
			if messages := result.Details().ExceptionMessages; len(messages) != 1 || messages[0] != "An internal error occurred while processing the request. See server logs for details." {
				t.Fatal(messages)
			}
		})
	}
}

func TestBothPipelinesSuppressReturnedValuesOnErrorAndCancellation(t *testing.T) {
	cause := errors.New("private handler detail")
	var cr commands.Registry
	var qr queries.Registry
	pipelineMust(t, commands.Register[pipelineCommand](&cr, commands.Handle(func(pipelineCommand, context.Context) (row, error) { return row{"secret", "secret"}, cause })))
	pipelineMust(t, queries.Register[row](&qr, "Read", queries.Function(func(context.Context, queries.NoArguments) (row, error) { return row{"secret", "secret"}, cause })))
	cp, err := cr.Build(commands.PipelineOptions{})
	pipelineMust(t, err)
	qp, err := qr.Build(queries.PipelineOptions{})
	pipelineMust(t, err)
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(pipelineContext(t))
		want := cause
		if canceled {
			cancel()
			want = context.Canceled
		}
		command, commandErr := cp.Execute(ctx, pipelineCommand{})
		query, queryErr := qp.Perform(ctx, "row.Read", queries.RequestFor(queries.NoArguments{}, queries.Parameters{}))
		cancel()
		if !errors.Is(commandErr, want) || !errors.Is(queryErr, want) || command.IsSuccess() || query.IsSuccess() {
			t.Fatalf("errors = %v / %v", commandErr, queryErr)
		}
		assertPipelinePayloadAbsent(t, pipelineJSON(t, command), "response")
		assertPipelinePayloadAbsent(t, pipelineJSON(t, query), "data")
	}
}
