// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	c "github.com/cratis/arc.go/integrations/chronicle"
)

type Model struct{ Name string }
type modelReader struct {
	reads              int
	failure            error
	released, producer bool
}

func (r *modelReader) Model(typ reflect.Type) (c.ModelDescriptor, bool) {
	return c.ModelDescriptor{Type: typ, ID: "model", HasProducer: r.producer}, true
}
func (r *modelReader) ReadModel(_ context.Context, request c.ModelRequest) (c.ModelDocument, error) {
	r.reads++
	if r.failure != nil {
		err := r.failure
		r.failure = nil
		return c.ModelDocument{}, err
	}
	return c.ModelDocument{Value: Model{Name: request.Key}, Exists: true, Released: r.released}, nil
}
func TestModelCacheIsFrameLocalAndNeverCachesFailure(t *testing.T) {
	reader := &modelReader{released: true, producer: true, failure: errors.New("temporary read failure")}
	builder, err := arc.NewBuilder(arc.Options{})
	must(t, err)
	integration, err := c.New(c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
		return c.Coordinates{Store: "test", Namespace: "Default"}, nil
	}, Transactions: &fakeFactory{}, Events: catalog{}, Models: reader})
	must(t, err)
	must(t, c.BindReadModel[Model](integration, "model"))
	must(t, integration.Install(builder))
	must(t, commands.Register[Child](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Child) (commands.NoResponse, error) {
		value, err := commands.RequireReadModel[Model](ctx, inv)
		if value.Name != "child" {
			t.Error(value)
		}
		return commands.NoResponse{}, err
	})))
	must(t, commands.Register[Change](builder, commands.Prepare(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.Preparation[Model], error) {
		_, err := commands.RequireReadModel[Model](ctx, inv)
		if err == nil {
			t.Fatal("lost initial failure")
		}
		value, err := commands.RequireReadModel[Model](ctx, inv)
		return commands.Provided(value), err
	}, func(ctx context.Context, inv *commands.Invocation, _ Change, prepared Model) (commands.NoResponse, error) {
		value, err := commands.RequireReadModel[Model](ctx, inv)
		must(t, err)
		if value != prepared {
			t.Fatal(value, prepared)
		}
		_, err = inv.Pipeline().Execute(ctx, Child{ID: "child"})
		return commands.NoResponse{}, err
	})))
	result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "parent"})
	must(t, err)
	if !result.IsSuccess() || reader.reads != 3 {
		t.Fatal(result, reader.reads)
	}
}
func TestUnreleasedModelsFailAndQueryMarkerDoesNotGrantOwnership(t *testing.T) {
	reader := &modelReader{}
	builder, err := arc.NewBuilder(arc.Options{})
	must(t, err)
	integration, err := c.New(c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
		return c.Coordinates{Store: "test", Namespace: "Default"}, nil
	}, Transactions: &fakeFactory{}, Events: catalog{}, Models: reader})
	must(t, err)
	if err := c.BindReadModel[Model](integration, "model"); !errors.Is(err, c.ErrNotRegistered) {
		t.Fatal(err)
	}
	must(t, c.BindExternalReadModel[Model](integration, "model"))
	must(t, integration.Install(builder))
	must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (Model, error) {
		return commands.RequireReadModel[Model](ctx, inv)
	})))
	result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "key"})
	if result.IsSuccess() || !errors.Is(err, c.ErrInvalid) {
		t.Fatal(result, err)
	}
}
