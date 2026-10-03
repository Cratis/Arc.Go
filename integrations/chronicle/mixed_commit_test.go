// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	c "github.com/cratis/arc.go/integrations/chronicle"
)

type committedAppendObserver struct{}

func (committedAppendObserver) Subscribe(_ context.Context, _ c.Coordinates, _ correlation.ID, notify func(c.CommitResult, error)) (func(), error) {
	notify(c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}, nil)
	return func() {}, nil
}

func TestImmediateCommitAndDeferredRejectionReportMixedCommit(t *testing.T) {
	f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.NotCommitted}}}
	builder, _ := setup(t, f)
	integration, err := c.New(c.Options{
		StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
			return c.Coordinates{Store: "test", Namespace: "Default"}, nil
		}, Transactions: f, Events: catalog{}, Appends: committedAppendObserver{},
	})
	must(t, err)
	must(t, integration.Install(builder))
	must(t, commands.Register[Change](builder, commands.Handle(func(Change, context.Context) (Changed, error) {
		return Changed{Name: "deferred"}, nil
	}), commands.WithNoResponse[Change]()))
	result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
	var completion *commands.CompletionError
	if result.IsSuccess() || !errors.As(err, &completion) || result.Completion().Disposition != commands.MixedCommit || completion.Report.Disposition != commands.MixedCommit || f.commits != 1 {
		t.Fatal(result, err, f)
	}
}
