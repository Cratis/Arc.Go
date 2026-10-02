// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
)

func TestBoundExecutorRejectsConcurrentAdmission(t *testing.T) {
	var r commands.Registry
	entered, release := make(chan struct{}), make(chan struct{})
	must(t, commands.Register[Child](&r, commands.Void(func(_ Child, ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})))
	must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (commands.NoResponse, error) {
		bound := inv.Pipeline()
		done := make(chan error, 1)
		go func() { _, err := bound.Execute(ctx, Child{}); done <- err }()
		<-entered
		_, err := bound.Execute(ctx, Child{})
		if !errors.Is(err, commands.ErrConcurrentExecution) {
			t.Errorf("concurrent admission: %v", err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
		return commands.NoResponse{}, nil
	})))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Parent{})
	if !errors.Is(err, commands.ErrConcurrentExecution) || result.IsSuccess() {
		t.Fatal("ignored concurrent failure was not sticky", result.Details(), err)
	}
}
func TestCallbackJoinsIncorrectlyDetachedChildBeforeCompletion(t *testing.T) {
	var r commands.Registry
	entered, returned, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	must(t, commands.Register[Child](&r, commands.Void(func(_ Child, ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})))
	must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (commands.NoResponse, error) {
		go func() { _, _ = inv.Pipeline().Execute(ctx, Child{}) }()
		<-entered
		close(returned)
		return commands.NoResponse{}, nil
	})))
	must(t, r.AddExecutionScope("observe", func(context.Context, *execution.Scope) (commands.ExecutionScope, error) {
		return participant{begin: func(context.Context, *commands.Invocation) error { return nil }, complete: func(_ context.Context, inv *commands.Invocation, result commands.Result[any]) (commands.Result[commands.NoResponse], error) {
			close(completed)
			if result.IsSuccess() {
				t.Error("detached child lifetime became success")
			}
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}}, nil
	}))
	p := build(t, &r, commands.PipelineOptions{})
	done := make(chan error, 1)
	go func() { _, err := p.Execute(t.Context(), Parent{}); done <- err }()
	<-returned
	select {
	case <-completed:
		t.Fatal("completion raced admitted child")
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, commands.ErrConcurrentExecution) {
		t.Fatal(err)
	}
	<-completed
}

func TestConcurrentIndependentRoots(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register(&r, commands.Handle(Rename.Handle)))
	p := build(t, &r, commands.PipelineOptions{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := p.Execute(t.Context(), Rename{"parallel"})
			if err != nil || !result.IsSuccess() {
				t.Error(result.Details(), err)
			}
		})
	}
	wg.Wait()
}
func TestAncestorExecutorCannotBeReusedFromChild(t *testing.T) {
	var r commands.Registry
	var ancestor commands.Pipeline
	must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (commands.NoResponse, error) {
		ancestor = inv.Pipeline()
		_, _ = ancestor.Execute(ctx, Child{})
		return commands.NoResponse{}, nil
	})))
	must(t, commands.Register[Child](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Child) (commands.NoResponse, error) {
		_, err := ancestor.Execute(ctx, Child{})
		if !errors.Is(err, commands.ErrConcurrentExecution) {
			t.Fatal(err)
		}
		return commands.NoResponse{}, nil
	})))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Parent{})
	if !errors.Is(err, commands.ErrConcurrentExecution) || result.IsSuccess() {
		t.Fatal(result.Details(), err)
	}
}
