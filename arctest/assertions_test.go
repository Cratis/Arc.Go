// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arctest_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cratis/arc.go/arctest"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

// Embedding TB preserves its sealed contract; only the assertion methods used by
// these helpers are intercepted. Panic models Fatal's non-returning behavior.
type assertionTB struct{ testing.TB }

func (assertionTB) Helper()           {}
func (assertionTB) Fatal(args ...any) { panic(assertionFailure(fmt.Sprint(args...))) }
func (assertionTB) Fatalf(format string, args ...any) {
	panic(assertionFailure(fmt.Sprintf(format, args...)))
}

type assertionFailure string

func TestAssertionHelpersRejectFalsePositives(t *testing.T) {
	id := concepts.UUID{}
	success := commands.Success(id)
	cases := []struct {
		name, fragment string
		assert         func(testing.TB)
	}{
		{"pipeline-error", "pipeline error", func(tb testing.TB) { arctest.RequireSuccess(tb, success, errors.New("failed cleanup")) }},
		{"zero-command", "expected success", func(tb testing.TB) { arctest.RequireSuccess(tb, commands.Result[int]{}, nil) }},
		{"not-ready-query", "expected success", func(tb testing.TB) { arctest.RequireSuccess(tb, queries.NotReady[int](id), nil) }},
		{"absent-response", "none was present", func(tb testing.TB) { arctest.RequireResponse(tb, success, nil) }},
		{"present-zero-response", "expected no command response", func(tb testing.TB) { arctest.RequireNoResponse(tb, commands.WithResponse(id, 0), nil) }},
		{"absent-data", "none was present", func(tb testing.TB) {
			arctest.RequireData(tb, queries.NewResult(queries.Details{Ready: true, Authorized: true}, serialization.Optional[int]{}), nil)
		}},
		{"authorized", "authorization denial", func(tb testing.TB) { arctest.RequireUnauthorized(tb, success) }},
		{"no-findings", "none were present", func(tb testing.TB) { arctest.RequireValidationErrors(tb, nil) }},
		{"missing-dependency-only", "seed the missing dependency", func(tb testing.TB) {
			arctest.RequireValidationErrors(tb, []validation.Result{{Reason: validation.DependencyUnavailable}, {Reason: validation.DependencyUnavailable}})
		}},
		{"wrong-reason", "expected validation reason", func(tb testing.TB) {
			arctest.RequireValidationReason(tb, []validation.Result{{Reason: validation.ValidatorFailed}}, validation.DependencyUnavailable)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				failure, ok := recover().(assertionFailure)
				if !ok || !strings.Contains(string(failure), tc.fragment) {
					t.Fatalf("assertion failure = %q, want fragment %q", failure, tc.fragment)
				}
			}()
			tc.assert(assertionTB{})
		})
	}
}

func TestAssertionHelpersPreservePresentZerosAndNil(t *testing.T) {
	id := concepts.UUID{}
	if got := arctest.RequireResponse(t, commands.WithResponse(id, 0), nil); got != 0 {
		t.Fatal(got)
	}
	if got := arctest.RequireData(t, queries.Success[*int](id, nil), nil); got != nil {
		t.Fatal(got)
	}
	arctest.RequireValidationErrors(t, []validation.Result{{Reason: validation.DependencyUnavailable}, {Reason: validation.MalformedRequest}})
}
