// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	c "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

type InvalidChild struct {
	ID c.EventSourceID `json:"id"`
}

func (InvalidChild) Validate(context.Context) ([]validation.Result, error) {
	return []validation.Result{{Message: "invalid child", Severity: validation.Error}}, nil
}

func TestEarlyAggregateCommitRejectsIgnoredNestedFailures(t *testing.T) {
	for _, failure := range []string{"denied", "validation", "ancestor"} {
		t.Run(failure, func(t *testing.T) {
			f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
			builder, factory := aggregateSetup(t, f, &historyReader{})
			must(t, commands.Register[InvalidChild](builder, commands.Void(func(InvalidChild, context.Context) error {
				t.Fatal("invalid child handled")
				return nil
			})))
			must(t, commands.Register[Child](builder, commands.Void(func(Child, context.Context) error {
				t.Fatal("denied child handled")
				return nil
			}), commands.WithAuthorization[Child](metadata.Authorization{})))
			commitCalls := 0
			commit := func(ctx context.Context, inv *commands.Invocation) (commands.NoResponse, error) {
				aggregate, err := factory.Get(ctx, inv)
				must(t, err)
				must(t, aggregate.Apply(ctx, Changed{Name: "must not persist"}))
				switch failure {
				case "validation":
					_, _ = inv.Pipeline().Execute(ctx, InvalidChild{ID: "invalid"})
				case "denied":
					_, _ = inv.Pipeline().Execute(ctx, Child{ID: "denied"})
				}
				_, err = aggregate.Commit(ctx)
				commitCalls++
				if !errors.Is(err, commands.ErrExecutionFailed) || f.commits != 0 {
					t.Fatal("failed early guard", err, f)
				}
				return commands.NoResponse{}, nil // Deliberately ignore the commit failure.
			}
			must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.NoResponse, error) {
				if failure == "ancestor" {
					_, _ = inv.Pipeline().Execute(ctx, Child{ID: "denied"})
					_, _ = inv.Pipeline().Execute(ctx, PlainID{})
					return commands.NoResponse{}, nil
				}
				return commit(ctx, inv)
			})))
			must(t, commands.Register[PlainID](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ PlainID) (commands.NoResponse, error) {
				return commit(ctx, inv)
			})))
			result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
			if result.IsSuccess() || !errors.Is(err, commands.ErrExecutionFailed) || commitCalls != 1 || f.commits != 0 || f.rollbacks != 1 || len(f.entries) != 0 || result.Completion().Disposition == commands.Committed {
				t.Fatal(result, err, commitCalls, f)
			}
		})
	}
}

func TestEarlyAggregateCommitIncludesSuccessfulNestedExecution(t *testing.T) {
	for _, advisory := range []bool{false, true} {
		t.Run(map[bool]string{false: "execute", true: "advisory validation"}[advisory], func(t *testing.T) {
			f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
			builder, factory := aggregateSetup(t, f, &historyReader{})
			must(t, commands.Register[InvalidChild](builder, commands.Void(func(InvalidChild, context.Context) error {
				t.Fatal("validation-only child handled")
				return nil
			})))
			must(t, commands.Register[Child](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Child) (commands.NoResponse, error) {
				aggregate, err := factory.Get(ctx, inv)
				must(t, err)
				must(t, aggregate.Apply(ctx, Changed{Name: "child"}))
				commit, err := aggregate.Commit(ctx)
				must(t, err)
				if commit.Report.Disposition != commands.Committed || f.commits != 1 {
					t.Fatal(commit, f)
				}
				return commands.NoResponse{}, nil
			})))
			must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.NoResponse, error) {
				aggregate, err := factory.Get(ctx, inv)
				must(t, err)
				must(t, aggregate.Apply(ctx, Changed{Name: "parent"}))
				if advisory {
					result, err := inv.Pipeline().Validate(ctx, InvalidChild{ID: "invalid"})
					must(t, err)
					if result.IsSuccess() {
						t.Fatal("validation unexpectedly succeeded")
					}
				}
				result, err := inv.Pipeline().Execute(ctx, Child{ID: "b"})
				must(t, err)
				if !result.IsSuccess() {
					t.Fatal(result)
				}
				return commands.NoResponse{}, nil
			})))
			result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
			must(t, err)
			if !result.IsSuccess() || result.Completion().Disposition != commands.Committed || f.begins != 1 || f.commits != 1 || f.rollbacks != 0 || len(f.entries) != 2 || f.entries[0].Source != "a" || f.entries[1].Source != "b" {
				t.Fatal(result, f)
			}
		})
	}
}

func TestEarlyAggregateCommitReportSurvivesLaterHandlerFailure(t *testing.T) {
	f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
	builder, factory := aggregateSetup(t, f, &historyReader{})
	cause := errors.New("failed after persistence")
	must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (bool, error) {
		aggregate, err := factory.Get(ctx, inv)
		must(t, err)
		must(t, aggregate.Apply(ctx, Changed{Name: "persisted"}))
		_, err = aggregate.Commit(ctx)
		must(t, err)
		return false, cause
	})))
	result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
	_, responsePresent := result.Response()
	var completion *commands.CompletionError
	if result.IsSuccess() || responsePresent || !errors.Is(err, cause) || !errors.As(err, &completion) || result.Completion().Disposition != commands.Committed || f.commits != 1 || f.rollbacks != 0 || len(f.entries) != 1 {
		t.Fatal(result, err, f)
	}
}
