// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	c "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/internal/appendorigin"
	"github.com/cratis/chronicle.go/eventsequences"
)

type observationSubscription struct {
	callback func(c.CommitResult, error)
	origin   eventsequences.Origin
	active   bool
	calls    sync.WaitGroup
}
type notifyingFactory struct {
	mu            sync.Mutex
	subscriptions map[*observationSubscription]bool
	origins       int
}

func (f *notifyingFactory) NewAppendOrigin() any {
	f.mu.Lock()
	f.origins++
	f.mu.Unlock()
	return eventsequences.NewOrigin()
}

func (f *notifyingFactory) Subscribe(ctx context.Context, _ c.Coordinates, _ correlation.ID, callback func(c.CommitResult, error)) (func(), error) {
	origin, _ := appendorigin.From(ctx).(eventsequences.Origin)
	subscription := &observationSubscription{callback: callback, active: true, origin: origin}
	f.mu.Lock()
	if f.subscriptions == nil {
		f.subscriptions = make(map[*observationSubscription]bool)
	}
	f.subscriptions[subscription] = true
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		subscription.active = false
		delete(f.subscriptions, subscription)
		f.mu.Unlock()
		subscription.calls.Wait()
	}, nil
}
func (f *notifyingFactory) notify(origin eventsequences.Origin, result c.CommitResult, err error) {
	f.mu.Lock()
	var callbacks []*observationSubscription
	for subscription := range f.subscriptions {
		if subscription.active && origin != (eventsequences.Origin{}) && subscription.origin == origin {
			subscription.calls.Add(1)
			callbacks = append(callbacks, subscription)
		}
	}
	f.mu.Unlock()
	for _, subscription := range callbacks {
		subscription.callback(result, err)
		subscription.calls.Done()
	}
}
func (f *notifyingFactory) Begin(context.Context, c.Coordinates) (c.Participant, c.CompletionOwner, error) {
	owner := &notifyingOwner{factory: f}
	return owner, owner, nil
}

type notifyingOwner struct {
	factory *notifyingFactory
	reject  bool
}

func (o *notifyingOwner) Stage(_ context.Context, batch c.Batch) error {
	o.reject = batch.Entries[0].Event.(Changed).Name == "reject"
	return nil
}
func (o *notifyingOwner) Commit(context.Context) (c.CommitResult, error) {
	result := c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}
	var err error
	if o.reject {
		result.Report.Disposition = commands.NotCommitted
		err = c.ErrInvalid
	}
	o.factory.notify(eventsequences.NewOrigin(), result, err)
	return result, err
}
func (*notifyingOwner) Rollback() error { return nil }

type SharedCorrelationCommand struct {
	ID     c.EventSourceID
	First  bool
	Reject bool
}

func TestSameCorrelationOwnerCommitCannotPoisonAnotherCommand(t *testing.T) {
	for _, firstRejects := range []bool{true, false} {
		t.Run(map[bool]string{true: "rejected first", false: "committed first"}[firstRejects], func(t *testing.T) {
			factory := &notifyingFactory{}
			builder, err := arc.NewBuilder(arc.Options{})
			must(t, err)
			integration, err := c.New(c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
				return c.Coordinates{Store: "store", Namespace: "tenant", Sequence: "event-log"}, nil
			}, Transactions: factory, Events: catalog{}, Appends: factory})
			must(t, err)
			must(t, integration.Install(builder))
			secondEntered := make(chan struct{})
			firstFinished := make(chan struct{})
			must(t, commands.Register[SharedCorrelationCommand](builder, commands.Handle(func(command SharedCorrelationCommand, ctx context.Context) (Changed, error) {
				if command.First {
					select {
					case <-secondEntered:
					case <-ctx.Done():
						return Changed{}, ctx.Err()
					}
				} else {
					close(secondEntered)
					select {
					case <-firstFinished:
					case <-ctx.Done():
						return Changed{}, ctx.Err()
					}
				}
				name := "accept"
				if command.Reject {
					name = "reject"
				}
				return Changed{Name: name}, nil
			}), commands.WithNoResponse[SharedCorrelationCommand]()))
			app := start(t, builder)
			ctx := correlation.WithID(t.Context(), correlation.ID{1})
			type execution struct {
				result commands.Result[any]
				err    error
				reject bool
			}
			results := make(chan execution, 2)
			go func() {
				result, err := app.Commands().Execute(ctx, SharedCorrelationCommand{ID: "first", First: true, Reject: firstRejects})
				results <- execution{result, err, firstRejects}
				close(firstFinished)
			}()
			go func() {
				result, err := app.Commands().Execute(ctx, SharedCorrelationCommand{ID: "second", Reject: !firstRejects})
				results <- execution{result, err, !firstRejects}
			}()
			for range 2 {
				got := <-results
				if got.reject {
					if got.result.IsSuccess() || !errors.Is(got.err, c.ErrInvalid) || got.result.Completion().Disposition != commands.NotCommitted {
						t.Errorf("rejected execution = %+v, %v; completion = %+v", got.result.Details(), got.err, got.result.Completion())
					}
				} else if !got.result.IsSuccess() || got.err != nil || got.result.Completion().Disposition != commands.Committed {
					t.Errorf("accepted execution = %+v, %v; completion = %+v", got.result.Details(), got.err, got.result.Completion())
				}
			}
		})
	}
}
