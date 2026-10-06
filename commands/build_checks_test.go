// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/commands"
)

func TestBuildChecksSeeExactLateRegistrationsAndRetry(t *testing.T) {
	var registry commands.Registry
	missing := errors.New("missing pointer command")
	var retained commands.BuildView
	calls := 0
	must(t, registry.AddBuildCheck(func(view commands.BuildView) error {
		calls++
		retained = view
		if !view.ContainsCommandType(reflect.TypeFor[*PointerCommand]()) {
			return missing
		}
		if view.ContainsCommandType(reflect.TypeFor[PointerCommand]()) || view.ContainsCommandType(nil) {
			t.Fatal("inexact registration membership")
		}
		return nil
	}))
	if pipeline, err := registry.Build(commands.PipelineOptions{}); pipeline != nil || err != missing {
		t.Fatalf("Build = %v, %v; want nil, original check error", pipeline, err)
	}
	must(t, commands.Register[*PointerCommand](&registry))
	if retained.ContainsCommandType(reflect.TypeFor[*PointerCommand]()) {
		t.Fatal("retained snapshot changed after registration")
	}
	if !registry.ContainsCommandType(reflect.TypeFor[*PointerCommand]()) || registry.ContainsCommandType(reflect.TypeFor[PointerCommand]()) || registry.ContainsCommandType(nil) {
		t.Fatal("registry membership is not exact")
	}
	build(t, &registry, commands.PipelineOptions{})
	if calls != 2 || !retained.ContainsCommandType(reflect.TypeFor[*PointerCommand]()) {
		t.Fatalf("calls = %d, final snapshot = %v", calls, retained)
	}
	if err := registry.AddBuildCheck(func(commands.BuildView) error { return nil }); !errors.Is(err, commands.ErrFrozen) {
		t.Fatalf("frozen AddBuildCheck = %v", err)
	}
}

func TestBuildCheckOrderingAndEmptyMembership(t *testing.T) {
	var nilRegistry *commands.Registry
	var empty commands.BuildView
	if nilRegistry.ContainsCommandType(reflect.TypeFor[Clear]()) || empty.ContainsCommandType(reflect.TypeFor[Clear]()) {
		t.Fatal("empty membership returned true")
	}
	if err := nilRegistry.AddBuildCheck(func(commands.BuildView) error { return nil }); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	var registry commands.Registry
	if err := registry.AddBuildCheck(nil); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	var order []int
	failure := errors.New("invalid integration configuration")
	for index := range 3 {
		must(t, registry.AddBuildCheck(func(commands.BuildView) error {
			order = append(order, index)
			if index == 1 {
				return failure
			}
			return nil
		}))
	}
	if _, err := registry.Build(commands.PipelineOptions{}); err != failure {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatalf("check order = %v", order)
	}
}
