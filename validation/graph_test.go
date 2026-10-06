// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/validation"
)

func ExampleGraph_Validate() {
	type quantity int
	var registry validation.Registry
	err := validation.RegisterConcept(&registry, validation.ValidatorFunc[quantity](func(_ context.Context, value quantity) ([]validation.Result, error) {
		if value > 0 {
			return nil, nil
		}
		return []validation.Result{{Severity: validation.Error, Message: "Quantity must be positive.", Members: []string{"value"}}}, nil
	}))
	if err != nil {
		panic(err)
	}
	graph, err := registry.Build()
	if err != nil {
		panic(err)
	}
	findings, err := graph.Validate(context.Background(), nil, struct {
		Quantity quantity `json:"quantity"`
	}{})
	if err != nil {
		panic(err)
	}
	fmt.Println(findings[0].Members, findings[0].Message)
	// Output: [quantity] Quantity must be positive.
}

type email string
type graphNode struct {
	Address    email        `json:"address"`
	Skip       email        `json:"skip" validate:"skipConcept"`
	Children   []*graphNode `json:"children"`
	Cycle      *graphNode   `json:"cycle"`
	OnValidate func()       `json:"-"`
}

func (n *graphNode) Validate(context.Context) ([]validation.Result, error) {
	if n.OnValidate != nil {
		n.OnValidate()
	}
	return nil, nil
}

func emailGraph(t *testing.T) *validation.Graph {
	t.Helper()
	var registry validation.Registry
	if err := validation.RegisterConcept(&registry, validation.ValidatorFunc[email](func(context.Context, email) ([]validation.Result, error) {
		return []validation.Result{{Severity: validation.Error, Message: "invalid", Members: []string{"value"}}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	graph, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestGraphCyclesSharedIdentityAndConceptOwningPaths(t *testing.T) {
	graph := emailGraph(t)
	calls := 0
	child := &graphNode{OnValidate: func() { calls++ }}
	root := &graphNode{Children: []*graphNode{child, nil, child, {OnValidate: func() { calls++ }}}}
	root.Cycle = root
	results, err := graph.Validate(t.Context(), nil, root)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("validated shared/equal instances %d times", calls)
	}
	var paths []string
	for _, result := range results {
		paths = append(paths, result.Members...)
	}
	if !reflect.DeepEqual(paths, []string{"address", "children.address", "children.address"}) {
		t.Fatal(paths)
	}
}

type conventionValue struct {
	Calls *int `json:"-"`
}

func (m *conventionValue) Validate(context.Context) ([]validation.Result, error) {
	*m.Calls++
	return nil, nil
}

func TestGraphDoesNotSynthesizePointerReceivers(t *testing.T) {
	var graph validation.Graph
	calls := 0
	model := conventionValue{Calls: &calls}
	if _, err := graph.Validate(t.Context(), nil, model); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("synthesized pointer receiver")
	}
	if _, err := graph.Validate(t.Context(), nil, &model); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("pointer convention not run")
	}
}

func TestGraphNodeValidatorsPrecedeMethodsAndChildren(t *testing.T) {
	var registry validation.Registry
	var order []string
	if err := validation.Register(&registry, validation.ValidatorFunc[*graphNode](func(context.Context, *graphNode) ([]validation.Result, error) {
		order = append(order, "registry")
		return nil, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := validation.RegisterConcept(&registry, validation.ValidatorFunc[email](func(context.Context, email) ([]validation.Result, error) {
		order = append(order, "concept")
		return nil, nil
	})); err != nil {
		t.Fatal(err)
	}
	graph, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Validate(t.Context(), nil, &graphNode{OnValidate: func() { order = append(order, "method") }}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"registry", "method", "concept"}) {
		t.Fatal(order)
	}
}

func TestSkipConceptDoesNotSuppressOrdinaryNodesOrDescendants(t *testing.T) {
	graph := emailGraph(t)
	type parent struct {
		Child      *graphNode `json:"child" validate:"skipConcept"`
		Collection []email    `json:"collection" validate:"skipConcept"`
	}
	calls := 0
	results, err := graph.Validate(t.Context(), nil, parent{Child: &graphNode{OnValidate: func() { calls++ }}, Collection: []email{"a", "b"}})
	if err != nil || calls != 1 {
		t.Fatalf("calls/error = %d/%v", calls, err)
	}
	var paths []string
	for _, result := range results {
		paths = append(paths, result.Members...)
	}
	if !reflect.DeepEqual(paths, []string{"child.address", "collection", "collection"}) {
		t.Fatal(paths)
	}
}

type EmbeddedGraphModel struct {
	Address email `json:"address"`
}

func TestGraphValidatesEmbeddedNodeBeforePromotedChildren(t *testing.T) {
	var registry validation.Registry
	var order []string
	if err := validation.Register(&registry, validation.ValidatorFunc[EmbeddedGraphModel](func(context.Context, EmbeddedGraphModel) ([]validation.Result, error) {
		order = append(order, "embedded")
		return nil, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := validation.RegisterConcept(&registry, validation.ValidatorFunc[email](func(context.Context, email) ([]validation.Result, error) {
		order = append(order, "concept")
		return []validation.Result{{Severity: validation.Error, Members: []string{"value"}}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	graph, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	results, err := graph.Validate(t.Context(), nil, struct{ EmbeddedGraphModel }{})
	if err != nil || !reflect.DeepEqual(order, []string{"embedded", "concept"}) || len(results) != 1 || !reflect.DeepEqual(results[0].Members, []string{"address"}) {
		t.Fatalf("order/results/error = %v/%v/%v", order, results, err)
	}
}

func TestGraphDepthAndOpaqueMaps(t *testing.T) {
	graph := emailGraph(t)
	root := &graphNode{}
	last := root
	for range 64 {
		last.Cycle = &graphNode{}
		last = last.Cycle
	}
	if _, err := graph.Validate(t.Context(), nil, root); !errors.Is(err, validation.ErrGraphDepth) {
		t.Fatal(err)
	}
	results, err := graph.Validate(t.Context(), nil, map[string]email{"key": "invalid"})
	if err != nil || len(results) != 0 {
		t.Fatalf("map traversed: %v %v", results, err)
	}
}

func TestGraphFailurePathsAndSafeInvocation(t *testing.T) {
	cause := errors.New("secret")
	for _, callback := range []validation.ValidatorFunc[email]{
		func(context.Context, email) ([]validation.Result, error) {
			return nil, fmt.Errorf("wrapped: %w", validation.Reject(validation.Result{Severity: validation.Error, Message: "rejected", Members: []string{"value"}}))
		},
		func(context.Context, email) ([]validation.Result, error) { return nil, cause },
		func(context.Context, email) ([]validation.Result, error) { panic("secret") },
	} {
		var registry validation.Registry
		if err := validation.RegisterConcept(&registry, callback); err != nil {
			t.Fatal(err)
		}
		graph, err := registry.Build()
		if err != nil {
			t.Fatal(err)
		}
		results, err := graph.Validate(t.Context(), nil, struct {
			Email email `json:"email"`
		}{})
		classified := pipeline.Classify(err)
		if len(results) != 0 || len(classified.Findings) != 1 || len(classified.Exceptions) != 0 || !reflect.DeepEqual(classified.Findings[0].Members, []string{"email"}) {
			t.Fatalf("results=%v classified=%+v err=%v", results, classified, err)
		}
		if classified.Findings[0].Reason == validation.ValidatorFailed && classified.Findings[0].Message != "The value could not be validated." {
			t.Fatal("unsafe finding")
		}
	}
}

type graphContext struct {
	context.Context
	values context.Context
}

func (ctx *graphContext) Value(key any) any { return ctx.values.Value(key) }

func TestGraphRechecksSecurityBetweenCallbacks(t *testing.T) {
	base := t.Context()
	ctx := &graphContext{Context: base, values: base}
	var registry validation.Registry
	if err := validation.Register(&registry, validation.ValidatorFunc[*graphNode](func(context.Context, *graphNode) ([]validation.Result, error) {
		ctx.values = identity.WithPrincipal(base, identity.Principal{})
		return nil, nil
	})); err != nil {
		t.Fatal(err)
	}
	graph, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := execution.OpenScope(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = graph.Validate(ctx, scope, &graphNode{OnValidate: func() { t.Error("method after identity changed") }})
	if !errors.Is(err, execution.ErrIdentityChanged) {
		t.Fatal(err)
	}
	if err := scope.Close(base); err != nil {
		t.Fatal(err)
	}
}

func TestGraphCancellationIsNotValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var registry validation.Registry
	wrapped := fmt.Errorf("original: %w", context.Canceled)
	if err := validation.Register(&registry, validation.ValidatorFunc[int](func(context.Context, int) ([]validation.Result, error) { cancel(); return nil, wrapped })); err != nil {
		t.Fatal(err)
	}
	graph, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	results, err := graph.Validate(ctx, nil, 1)
	if len(results) != 0 || !errors.Is(err, wrapped) {
		t.Fatal(err)
	}
	var failure validation.Failure
	if errors.As(err, &failure) {
		t.Fatal("cancellation became validation")
	}
}
