// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/queries"
)

type countedResources struct{ closes *atomic.Int32 }

func (r countedResources) Close(context.Context) error { r.closes.Add(1); return nil }
func TestIndependentQueryRootsSupportConcurrentCalls(t *testing.T) {
	var r queries.Registry
	var opens, closes atomic.Int32
	mustRegister(t, queries.Register[Item](&r, "Current", itemPerformer(), public[queries.NoArguments]()))
	p := build(t, &r, queries.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) {
		opens.Add(1)
		return countedResources{&closes}, nil
	}})
	var joined sync.WaitGroup
	for range 20 {
		joined.Go(func() {
			result, err := p.Perform(context.Background(), "Item.Current", queries.Request{})
			if err != nil || !result.IsSuccess() {
				t.Errorf("result=%+v, %v", result.Details(), err)
			}
		})
	}
	joined.Wait()
	if opens.Load() != 20 || closes.Load() != 20 {
		t.Fatalf("opened=%d closed=%d", opens.Load(), closes.Load())
	}
}
func TestBorrowedQueryAdmissionJoinsBeforeOwnedScopeDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var r queries.Registry
		started, release := make(chan struct{}), make(chan struct{})
		var closes atomic.Int32
		mustRegister(t, queries.Register[Item](&r, "Current", queries.Function(func(context.Context, queries.NoArguments) (Item, error) {
			close(started)
			<-release
			if closes.Load() != 0 {
				t.Error("resources closed during performer")
			}
			return Item{}, nil
		}), public[queries.NoArguments]()))
		p := build(t, &r, queries.PipelineOptions{})
		s, err := execution.OpenScope(context.Background(), func(context.Context) (execution.Resources, error) { return countedResources{&closes}, nil })
		mustRegister(t, err)
		performed := make(chan struct{})
		go func() {
			defer close(performed)
			result, err := p.PerformScoped(context.Background(), s, "Item.Current", queries.Request{})
			// Close stops new stage admissions while joining the entered callback.
			if !errors.Is(err, execution.ErrScopeClosed) || result.IsSuccess() {
				t.Errorf("result=%+v %v", result.Details(), err)
			}
			if _, present := result.Data(); present {
				t.Error("shutdown published data")
			}
		}()
		<-started
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			if err := s.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		synctest.Wait()
		if closes.Load() != 0 {
			t.Fatal("scope disposed before query returned")
		}
		close(release)
		<-performed
		<-closed
		if closes.Load() != 1 {
			t.Fatal("scope cleanup count")
		}
	})
}
