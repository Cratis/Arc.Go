// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/services"
)

type resource struct{ close func(context.Context) error }

func (r *resource) Close(ctx context.Context) error { return r.close(ctx) }

type dependent struct{ *resource }
type rootResource struct{ *resource }
type borrowedResource struct{ *resource }
type transientResource struct{ *resource }
type plainCloser struct{ close func() error }

func (r *plainCloser) Close() error { return r.close() }

func TestReverseCleanupRootTransientsAndBorrowedValues(t *testing.T) {
	ctx := context.Background()
	r := &services.Registry{}
	var order []string
	makeResource := func(name string) *resource {
		return &resource{close: func(context.Context) error { order = append(order, name); return nil }}
	}
	register(t, r, services.Scoped, func(context.Context, *services.Scope) (*resource, error) { return makeResource("dependency"), nil })
	register(t, r, services.Scoped, func(ctx context.Context, s *services.Scope) (*dependent, error) {
		_, err := services.Resolve[*resource](ctx, s)
		return &dependent{makeResource("parent")}, err
	}, services.KeyFor[*resource]())
	register(t, r, services.Transient, func(context.Context, *services.Scope) (*transientResource, error) {
		return &transientResource{makeResource("root transient")}, nil
	})
	register(t, r, services.Singleton, func(ctx context.Context, s *services.Scope) (*rootResource, error) {
		_, err := services.Resolve[*transientResource](ctx, s)
		return &rootResource{makeResource("singleton")}, err
	}, services.KeyFor[*transientResource]())
	if err := services.BindValue(r, &borrowedResource{makeResource("borrowed")}); err != nil {
		t.Fatal(err)
	}
	p, err := r.Build()
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.NewScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolved[*dependent](t, ctx, s)
	resolved[*rootResource](t, ctx, s)
	resolved[*borrowedResource](t, ctx, s)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"parent", "dependency"}) {
		t.Fatal(order)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"parent", "dependency", "singleton", "root transient"}) {
		t.Fatal(order)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(order) != 4 {
		t.Fatal("double cleanup", order)
	}
}
func TestProviderClosesScopesInReverseOpeningOrder(t *testing.T) {
	ctx := context.Background()
	r := &services.Registry{}
	var order []int
	next := 0
	register(t, r, services.Scoped, func(context.Context, *services.Scope) (*resource, error) {
		next++
		n := next
		return &resource{close: func(context.Context) error { order = append(order, n); return nil }}, nil
	})
	p, err := r.Build()
	if err != nil {
		t.Fatal(err)
	}
	s1, err := p.NewScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := p.NewScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolved[*resource](t, ctx, s1)
	resolved[*resource](t, ctx, s2)
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []int{2, 1}) {
		t.Fatal(order)
	}
	if _, err := p.NewScope(ctx); !errors.Is(err, services.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := services.Resolve[*resource](ctx, s1); !errors.Is(err, services.ErrClosed) {
		t.Fatal(err)
	}
}
func TestPartialFailureImmediateCleanupAndNilValues(t *testing.T) {
	ctx := context.Background()
	r := &services.Registry{}
	failure := errors.New("factory failure")
	cleanup := errors.New("cleanup failure")
	var order []string
	register(t, r, services.Scoped, func(context.Context, *services.Scope) (*resource, error) {
		return &resource{close: func(context.Context) error { order = append(order, "dependency"); return nil }}, nil
	})
	register(t, r, services.Scoped, func(ctx context.Context, s *services.Scope) (*dependent, error) {
		_, err := services.Resolve[*resource](ctx, s)
		if err != nil {
			return nil, err
		}
		return &dependent{&resource{close: func(context.Context) error { order = append(order, "failed parent"); return cleanup }}}, failure
	}, services.KeyFor[*resource]())
	register(t, r, services.Transient, func(context.Context, *services.Scope) (*alpha, error) { return nil, nil })
	p, err := r.Build()
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.NewScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.Resolve[*dependent](ctx, s); !errors.Is(err, failure) || !errors.Is(err, cleanup) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"failed parent"}) {
		t.Fatal(order)
	}
	if _, err := services.Resolve[*alpha](ctx, s); !errors.Is(err, services.ErrNilValue) {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"failed parent", "dependency"}) {
		t.Fatal(order)
	}
}
func TestCleanupContinuesAfterErrorsAndPanics(t *testing.T) {
	ctx := context.Background()
	r := &services.Registry{}
	first := errors.New("first")
	second := errors.New("second")
	calls := 0
	register(t, r, services.Transient, func(context.Context, *services.Scope) (*resource, error) {
		calls++
		n := calls
		return &resource{close: func(context.Context) error {
			switch n {
			case 1:
				return first
			case 2:
				panic("secret")
			default:
				return second
			}
		}}, nil
	})
	p, err := r.Build()
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.NewScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		resolved[*resource](t, ctx, s)
	}
	err = s.Close(ctx)
	if !errors.Is(err, first) || !errors.Is(err, second) || !errors.Is(err, services.ErrCallbackPanicked) {
		t.Fatal(err)
	}
	var diagnostic *services.Error
	if !errors.As(err, &diagnostic) {
		t.Fatal(err)
	}
	if again := s.Close(ctx); again != err {
		t.Fatal("result not retained")
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestCloserPreferenceAndPlainCloser(t *testing.T) {
	r := &services.Registry{}
	ctx := context.Background()
	contextCalls, ioCalls := 0, 0
	register(t, r, services.Scoped, func(context.Context, *services.Scope) (*resource, error) {
		return &resource{close: func(context.Context) error { contextCalls++; return nil }}, nil
	})
	register(t, r, services.Scoped, func(context.Context, *services.Scope) (*plainCloser, error) {
		return &plainCloser{close: func() error { ioCalls++; return nil }}, nil
	})
	p := provider(t, r)
	s := scope(t, p, ctx)
	resolved[*resource](t, ctx, s)
	resolved[*plainCloser](t, ctx, s)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if contextCalls != 1 || ioCalls != 1 {
		t.Fatal(contextCalls, ioCalls)
	}
}
