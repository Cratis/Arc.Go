// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

func TestGuardedStateSharesOnlyBoundRootAndKeepsFramesIsolated(t *testing.T) {
	var r commands.Registry
	rootKey, frameKey := commands.NewStateKey[int](), commands.NewStateKey[int]()
	var pipeline commands.Pipeline
	var retained *commands.Invocation
	var retainedContext context.Context
	children := 0
	must(t, commands.Register[Parent](&r, commands.Prepare(
		func(ctx context.Context, inv *commands.Invocation, _ Parent) (commands.Preparation[int], error) {
			if _, found, err := commands.RootState(ctx, inv, rootKey); err != nil || found {
				t.Fatal("previous execution retained state", found, err)
			}
			must(t, commands.SetRootState(ctx, inv, rootKey, 41))
			must(t, commands.SetFrameState(ctx, inv, frameKey, 7))
			retained, retainedContext = inv, ctx
			return commands.Provided(0), nil
		},
		func(ctx context.Context, inv *commands.Invocation, _ Parent, _ int) (commands.NoResponse, error) {
			if _, _, err := commands.FrameState(retainedContext, retained, frameKey); !errors.Is(err, commands.ErrExecutionClosed) {
				t.Fatal("Provide capability survived callback", err)
			}
			for range 2 {
				result, err := inv.Pipeline().Execute(ctx, Child{})
				must(t, err)
				if !result.IsSuccess() {
					t.Fatal(result.Details())
				}
			}
			if value, found, err := commands.FrameState(ctx, inv, frameKey); err != nil || !found || value != 7 {
				t.Fatal("child overwrote parent frame", value, found, err)
			}
			if value, _, err := commands.RootState(ctx, inv, rootKey); err != nil || value != 43 {
				t.Fatal("children did not share root", value, err)
			}
			result, err := pipeline.Execute(ctx, Grandchild{})
			must(t, err)
			if !result.IsSuccess() {
				t.Fatal(result.Details())
			}
			return commands.NoResponse{}, nil
		})))
	must(t, commands.Register[Child](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Child) (commands.NoResponse, error) {
		children++
		if _, found, err := commands.FrameState(ctx, inv, frameKey); err != nil || found {
			t.Fatal("child inherited parent/sibling frame", found, err)
		}
		must(t, commands.SetFrameState(ctx, inv, frameKey, 99))
		value, found, err := commands.RootState(ctx, inv, rootKey)
		must(t, err)
		if !found {
			t.Fatal("missing root state")
		}
		must(t, commands.SetRootState(ctx, inv, rootKey, value+1))
		parent, present, err := inv.ParentCommandContext(ctx)
		must(t, err)
		if !present || parent.Descriptor().Type.Name != "Parent" || parent.CorrelationID() != inv.CommandContext().CorrelationID() {
			t.Fatal("wrong parent metadata", parent, present)
		}
		return commands.NoResponse{}, nil
	})))
	must(t, commands.Register[Grandchild](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Grandchild) (commands.NoResponse, error) {
		if _, found, err := commands.RootState(ctx, inv, rootKey); err != nil || found {
			t.Fatal(found, err)
		}
		if _, found, err := inv.ParentCommandContext(ctx); err != nil || found {
			t.Fatal(found, err)
		}
		return commands.NoResponse{}, nil
	})))
	pipeline = build(t, &r, commands.PipelineOptions{})
	for range 2 {
		result, err := pipeline.Execute(t.Context(), Parent{})
		must(t, err)
		if !result.IsSuccess() {
			t.Fatal(result.Details())
		}
	}
	if children != 4 {
		t.Fatal(children)
	}
}

func TestStateGuardsExpirySecurityCorrelationAndDistinctKeys(t *testing.T) {
	var r commands.Registry
	key, other := commands.NewStateKey[any](), commands.NewStateKey[any]()
	var retained *commands.Invocation
	var retainedContext context.Context
	must(t, commands.Register[Clear](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Clear) (commands.NoResponse, error) {
		retained, retainedContext = inv, ctx
		must(t, commands.SetRootState(ctx, inv, key, nil))
		if value, found, err := commands.RootState(ctx, inv, key); err != nil || !found || value != nil {
			t.Fatal(value, found, err)
		}
		if _, found, err := commands.RootState(ctx, inv, other); err != nil || found {
			t.Fatal(found, err)
		}
		if err := commands.SetRootState(ctx, inv, commands.StateKey[int]{}, 1); !errors.Is(err, commands.ErrInvalidRegistration) {
			t.Fatal(err)
		}
		for _, changed := range []context.Context{identity.WithPrincipal(ctx, identity.System()), tenancy.WithTenant(ctx, tenancy.Default())} {
			if _, _, err := commands.RootState(changed, inv, key); !errors.Is(err, execution.ErrIdentityChanged) {
				t.Fatal(err)
			}
			if err := commands.SetFrameState(changed, inv, key, "secret"); !errors.Is(err, execution.ErrIdentityChanged) {
				t.Fatal(err)
			}
		}
		if _, _, err := commands.RootState(correlation.WithID(ctx, correlation.ID{}), inv, key); !errors.Is(err, commands.ErrExecutionMismatch) {
			t.Fatal(err)
		}
		return commands.NoResponse{}, nil
	})))
	p := build(t, &r, commands.PipelineOptions{})
	_, err := p.Execute(t.Context(), Clear{})
	must(t, err)
	if err := commands.SetRootState(retainedContext, retained, key, "late"); !errors.Is(err, commands.ErrExecutionClosed) {
		t.Fatal(err)
	}
	if _, _, err := retained.ParentCommandContext(retainedContext); !errors.Is(err, commands.ErrExecutionClosed) {
		t.Fatal(err)
	}
}

func TestParentStateAccessRejectedWhileNestedFrameRuns(t *testing.T) {
	var r commands.Registry
	key := commands.NewStateKey[int]()
	entered, release := make(chan struct{}), make(chan struct{})
	must(t, commands.Register[Child](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Child) (commands.NoResponse, error) {
		close(entered)
		<-release
		return commands.NoResponse{}, nil
	})))
	must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (commands.NoResponse, error) {
		done := make(chan error, 1)
		go func() { _, err := inv.Pipeline().Execute(ctx, Child{}); done <- err }()
		<-entered
		_, _, err := commands.RootState(ctx, inv, key)
		if !errors.Is(err, commands.ErrExecutionMismatch) {
			t.Error(err)
		}
		close(release)
		return commands.NoResponse{}, <-done
	})))
	p := build(t, &r, commands.PipelineOptions{})
	_, err := p.Execute(t.Context(), Parent{})
	must(t, err)
}
