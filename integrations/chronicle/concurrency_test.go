// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	c "github.com/cratis/arc.go/integrations/chronicle"
)

type scopePolicy struct{ requests []c.ScopeRequest }

func (p *scopePolicy) ResolveScope(_ context.Context, request c.ScopeRequest) (c.LabeledScope, error) {
	p.requests = append(p.requests, request)
	position := uint64(1)
	if request.Filter.Source == "B" {
		position = 9
	}
	return c.LabeledScope{Label: string(request.Filter.Source), Filter: request.Filter, Expectation: c.Expectation{Kind: c.UpperBound, Position: position}}, nil
}
func TestConcurrencyPolicyResolvesActualTargetsAndCachesRepeatedSource(t *testing.T) {
	policy := &scopePolicy{}
	f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
	builder, err := arc.NewBuilder(arc.Options{})
	must(t, err)
	integration, err := c.New(c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
		return c.Coordinates{Store: "test", Namespace: "Default"}, nil
	}, Transactions: f, Events: catalog{}, Concurrency: policy})
	must(t, err)
	must(t, c.ConfigureCommand[Change](integration, c.CommandOptions{Route: c.Route{SourceType: "ignored-source", StreamType: "selected-stream", StreamID: "ignored-id"}, ConcurrencyStreamType: true}))
	must(t, integration.Install(builder))
	must(t, commands.Register[Change](builder, commands.Handle(func(Change, context.Context) (c.EventBatch, error) {
		return c.Events(Changed{Name: "A1"}, c.EventForSource("B", Changed{Name: "B1"}), Changed{Name: "A2"}), nil
	}), commands.WithNoResponse[Change]()))
	result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "A"})
	must(t, err)
	if !result.IsSuccess() || len(policy.requests) != 2 || len(f.scopes) != 2 || f.scopes[0].Expectation.Position != 1 || f.scopes[1].Expectation.Position != 9 {
		t.Fatal(result, policy, f)
	}
	for _, request := range policy.requests {
		if request.Filter.Route != (c.Route{StreamType: "selected-stream"}) {
			t.Fatal(request)
		}
	}
}
func TestExplicitLifecycleOwnershipIsNotImplicitBuildWork(t *testing.T) {
	starts, closes := 0, 0
	integration, err := c.New(c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
		return c.Coordinates{Store: "test", Namespace: "Default"}, nil
	}, Transactions: &fakeFactory{}, Events: catalog{}, Start: func(context.Context) error { starts++; return nil }, Close: func() error { closes++; return nil }})
	must(t, err)
	builder, err := arc.NewBuilder(arc.Options{})
	must(t, err)
	must(t, integration.Install(builder))
	_ = start(t, builder)
	if starts != 0 || closes != 0 {
		t.Fatal("implicit lifecycle", starts, closes)
	}
	must(t, integration.Start(t.Context()))
	must(t, integration.Close())
	must(t, integration.Close())
	if starts != 1 || closes != 1 {
		t.Fatal(starts, closes)
	}
}
