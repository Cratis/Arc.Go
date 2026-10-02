// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

func TestQueryStatusPrecedence(t *testing.T) {
	for _, ready := range []bool{false, true} {
		for _, authorized := range []bool{false, true} {
			for _, valid := range []bool{false, true} {
				for _, exceptions := range []bool{false, true} {
					d := queries.Details{Ready: ready, Authorized: authorized}
					if !valid {
						d.ValidationResults = []validation.Result{{Severity: validation.Warning}}
					}
					if exceptions {
						d.ExceptionMessages = []string{"error"}
					}
					result := queries.NewResult(d, serialization.Optional[int]{})
					want := 200
					if !authorized {
						want = 403
					} else if !valid {
						want = 400
					} else if !ready {
						want = 202
					} else if exceptions {
						want = 500
					}
					if result.StatusCode() != want {
						t.Fatalf("status = %d, want %d for %#v", result.StatusCode(), want, d)
					}
				}
			}
		}
	}
}

func TestNullIsReadyAndAbsentIsPending(t *testing.T) {
	result := queries.Success[*int](concepts.UUID{}, nil)
	value, present := result.Data()
	if !present || value != nil || !result.IsSuccess() {
		t.Fatal("null emission is not ready")
	}
	pending := queries.NotReady[*int](concepts.UUID{})
	if _, present = pending.Data(); present || pending.IsReady() {
		t.Fatal("no emission treated as a value")
	}
}

func TestQueryOwnsSlicesButNotApplicationItems(t *testing.T) {
	detail := "original"
	changes := &queries.ChangeSet{Added: []any{"one"}}
	d := queries.Details{ValidationResults: []validation.Result{{Members: []string{"name"}, ReasonDetail: &detail}}, ExceptionMessages: []string{"safe"}, ChangeSet: changes}
	result := queries.NewResult(d, serialization.Optional[int]{})
	changes.Added[0] = "changed"
	d.ExceptionMessages[0] = "changed"
	d.ValidationResults[0].Members[0] = "changed"
	copy := result.Details()
	copy.ChangeSet.Added[0] = "changed again"
	copy.ValidationResults[0].Members[0] = "changed again"
	got := result.Details()
	if got.ChangeSet.Added[0] != "one" || got.ValidationResults[0].Members[0] != "name" || got.ExceptionMessages[0] != "safe" {
		t.Fatalf("mutated details %#v", got)
	}
	if _, err := json.Marshal(queries.Success(concepts.UUID{}, make(chan int))); err == nil {
		t.Fatal("unsupported data silently serialized")
	}
	for _, changes := range []queries.ChangeSet{{Added: []any{make(chan int)}}, {Replaced: []any{make(chan int)}}, {Removed: []any{make(chan int)}}} {
		if _, err := json.Marshal(changes); err == nil {
			t.Fatal("unsupported change-set item silently serialized")
		}
	}
}

func TestPagingInfo(t *testing.T) {
	for _, tc := range []struct {
		value queries.PagingInfo
		want  int32
	}{
		{queries.PagingInfo{}, 0}, {queries.PagingInfo{TotalItems: 45}, 0}, {queries.PagingInfo{Size: 25, TotalItems: 51}, 3},
		{queries.PagingInfo{Size: 25, TotalItems: 50}, 2}, {queries.PagingInfo{Size: 25, TotalItems: 0}, 0},
		{queries.PagingInfo{Size: -25, TotalItems: 51}, -2}, {queries.PagingInfo{Size: 1, TotalItems: math.MaxInt64}, math.MinInt32},
	} {
		if got := tc.value.TotalPages(); got != tc.want {
			t.Errorf("%#v pages = %d, want %d", tc.value, got, tc.want)
		}
	}
}
