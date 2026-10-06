// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

func TestMergeAndJoinedFailureClassification(t *testing.T) {
	id, err := correlation.Parse("2d160f30-a061-4c0d-9a0d-b8aa6cfc9e8c")
	must(t, err)
	cause := errors.New("cleanup secret")
	joined := errors.Join(validation.Reject(validation.Result{Severity: validation.Error, Message: "safe rule"}), cause)
	fragment := commands.FromError[commands.NoResponse](correlation.ID{}, joined)
	result := commands.Merge(commands.WithResponse(id, 42), fragment, commands.Unauthorized(correlation.ID{}, "first"), commands.Unauthorized(correlation.ID{}, "second"))
	if result.IsAuthorized() || result.IsValid() || !result.HasExceptions() || result.Details().CorrelationID != id || result.Details().AuthorizationFailureReason != "first" {
		t.Fatal(result.Details())
	}
	if _, ok := result.Response(); ok {
		t.Fatal("merge retained failed response")
	}
	if messages := result.Details().ExceptionMessages; len(messages) != 1 || messages[0] == cause.Error() {
		t.Fatal(messages)
	}
	denied := commands.FromError[any](id, authorization.Deny("local secret").Err())
	if denied.IsAuthorized() || denied.HasExceptions() {
		t.Fatal(denied.Details())
	}
}
func TestExceptionFragmentsAreRedacted(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r))
	must(t, r.AddFilter("unsafe fragment", func(context.Context, *execution.Scope) (commands.Filter, error) {
		return commands.FilterFunc(func(_ context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			return commands.NewResult(commands.Details{Authorized: true, CorrelationID: inv.CommandContext().CorrelationID(), ExceptionMessages: []string{"secret"}, ExceptionStackTrace: "secret stack"}, serialization.Optional[commands.NoResponse]{}), nil
		}), nil
	}))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Clear{})
	must(t, err)
	if !result.HasExceptions() || result.Details().ExceptionMessages[0] == "secret" || result.Details().ExceptionStackTrace != "" {
		t.Fatal(result.Details())
	}
}
func TestNonblockingProvideErrorCannotBecomeSuccessfulMissingPayload(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.WithProvide(func(Clear, context.Context) (int, error) {
		return 0, validation.Reject(validation.Result{Severity: validation.Warning})
	}, func(Clear, context.Context, int) (int, error) { t.Fatal("fabricated payload"); return 0, nil })))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Clear{})
	if result.IsSuccess() || !errors.Is(err, commands.ErrInvalidPreparation) {
		t.Fatal(result.Details(), err)
	}
}
