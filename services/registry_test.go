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

type alpha struct{}
type beta struct{}
type gamma struct{}
type contract interface{ Method() }

func bind[T any](t *testing.T, r *services.Registry, l services.Lifetime, deps ...services.Key) {
	t.Helper()
	if err := services.Bind(r, l, func(context.Context, *services.Scope) (T, error) { var value T; return value, nil }, deps...); err != nil {
		t.Fatal(err)
	}
}
func TestExactKeysAndRegistrationFailures(t *testing.T) {
	if services.KeyFor[alpha]() == services.KeyFor[*alpha]() || services.KeyFor[contract]() == services.KeyFor[*contract]() || services.KeyFor[alpha]().String() == services.KeyFor[*alpha]().String() {
		t.Fatal("keys collapsed")
	}
	r := &services.Registry{}
	if err := services.Bind[alpha](r, services.Singleton, nil); !errors.Is(err, services.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	if err := services.BindValue[*alpha](r, nil); !errors.Is(err, services.ErrNilValue) {
		t.Fatal(err)
	}
	if err := services.Bind[alpha](r, 0, func(context.Context, *services.Scope) (alpha, error) { return alpha{}, nil }); !errors.Is(err, services.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	if err := services.Bind[alpha](r, services.Scoped, func(context.Context, *services.Scope) (alpha, error) { return alpha{}, nil }, services.Key{}); !errors.Is(err, services.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	if err := services.Bind[alpha](r, services.Scoped, func(context.Context, *services.Scope) (alpha, error) { return alpha{}, nil }, services.KeyFor[beta](), services.KeyFor[beta]()); !errors.Is(err, services.ErrDuplicate) {
		t.Fatal(err)
	}
	bind[alpha](t, r, services.Scoped)
	if err := services.BindValue(r, alpha{}); !errors.Is(err, services.ErrDuplicate) {
		t.Fatal(err)
	}
	if _, err := r.Build(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Build(); !errors.Is(err, services.ErrFrozen) {
		t.Fatal(err)
	}
	if err := services.BindValue(r, beta{}); !errors.Is(err, services.ErrFrozen) {
		t.Fatal(err)
	}
}
func TestBuildDoesNotInvokeFactoriesAndFailureIsEditable(t *testing.T) {
	r := &services.Registry{}
	calls := 0
	deps := []services.Key{services.KeyFor[beta]()}
	if err := services.Bind(r, services.Scoped, func(context.Context, *services.Scope) (alpha, error) { calls++; return alpha{}, nil }, deps...); err != nil {
		t.Fatal(err)
	}
	deps[0] = services.KeyFor[gamma]()
	if _, err := r.Build(); !errors.Is(err, services.ErrMissing) {
		t.Fatal(err)
	}
	bind[beta](t, r, services.Transient)
	if _, err := r.Build(); err != nil || calls != 0 {
		t.Fatal(err, calls)
	}
	if _, err := (&services.Registry{}).Build(); err != nil {
		t.Fatal(err)
	}
}
func TestDependencyGraphs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		a, b, c services.Lifetime
		edges   [][]services.Key
		kind    error
		path    []services.Key
	}{
		{"self cycle", services.Scoped, services.Scoped, services.Scoped, [][]services.Key{{services.KeyFor[alpha]()}, nil, nil}, services.ErrCycle, []services.Key{services.KeyFor[alpha](), services.KeyFor[alpha]()}},
		{"multi cycle", services.Scoped, services.Scoped, services.Scoped, [][]services.Key{{services.KeyFor[beta]()}, {services.KeyFor[gamma]()}, {services.KeyFor[alpha]()}}, services.ErrCycle, []services.Key{services.KeyFor[alpha](), services.KeyFor[beta](), services.KeyFor[gamma](), services.KeyFor[alpha]()}},
		{"transitive captive", services.Singleton, services.Transient, services.Scoped, [][]services.Key{{services.KeyFor[beta]()}, {services.KeyFor[gamma]()}, nil}, services.ErrCaptiveLifetime, []services.Key{services.KeyFor[alpha](), services.KeyFor[beta](), services.KeyFor[gamma]()}},
		{"root transient", services.Singleton, services.Transient, services.Transient, [][]services.Key{{services.KeyFor[beta]()}, {services.KeyFor[gamma]()}, nil}, nil, nil},
		{"scoped transient", services.Scoped, services.Transient, services.Scoped, [][]services.Key{{services.KeyFor[beta]()}, {services.KeyFor[gamma]()}, nil}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &services.Registry{}
			bind[alpha](t, r, tc.a, tc.edges[0]...)
			bind[beta](t, r, tc.b, tc.edges[1]...)
			bind[gamma](t, r, tc.c, tc.edges[2]...)
			_, err := r.Build()
			if !errors.Is(err, tc.kind) {
				t.Fatal(err)
			}
			if err != nil {
				var diagnostic *services.Error
				if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic.Path, tc.path) {
					t.Fatal(err, diagnostic)
				}
			}
		})
	}
}
