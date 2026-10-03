// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arctest

import (
	"fmt"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/validation"
)

// RequireSuccess fails tb immediately for a pipeline error or unsuccessful command
// or query result. It never treats a default/zero result as success.
func RequireSuccess(tb testing.TB, result interface{ IsSuccess() bool }, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatalf("arctest: expected success, pipeline error: %v", err)
	}
	if !result.IsSuccess() {
		tb.Fatalf("arctest: expected success, result: %+v", result)
	}
}

// RequireResponse requires successful execution and a present response, returning
// it for ordinary Go assertions. A present zero, false or empty string is valid.
func RequireResponse[R any](tb testing.TB, result commands.Result[R], err error) R {
	tb.Helper()
	RequireSuccess(tb, result, err)
	response, present := result.Response()
	if !present {
		tb.Fatal("arctest: expected a command response, but none was present")
	}
	return response
}

// RequireNoResponse requires success and absence of a command response.
func RequireNoResponse[R any](tb testing.TB, result commands.Result[R], err error) {
	tb.Helper()
	RequireSuccess(tb, result, err)
	if _, present := result.Response(); present {
		tb.Fatal("arctest: expected no command response")
	}
}

// RequireData requires a successful snapshot and present data, returning it for
// ordinary Go assertions. A locally present nil is distinct from no data.
func RequireData[R any](tb testing.TB, result queries.Result[R], err error) R {
	tb.Helper()
	RequireSuccess(tb, result, err)
	data, present := result.Data()
	if !present {
		tb.Fatal("arctest: expected query data, but none was present")
	}
	return data
}

// RequireUnauthorized requires an authorization denial. Inspect the separately
// returned pipeline error when the specification also needs its identity.
func RequireUnauthorized(tb testing.TB, result interface{ IsAuthorized() bool }) {
	tb.Helper()
	if result.IsAuthorized() {
		tb.Fatal("arctest: expected authorization denial")
	}
}

// RequireValidationErrors requires findings that are not solely unresolved
// dependencies. A missing test dependency must not make a domain-rule test pass.
// Use RequireValidationReason to explicitly assert dependencyUnavailable.
func RequireValidationErrors(tb testing.TB, findings []validation.Result) {
	tb.Helper()
	if err := validationErrors(findings); err != nil {
		tb.Fatal(err)
	}
}

func validationErrors(findings []validation.Result) error {
	if len(findings) == 0 {
		return fmt.Errorf("arctest: expected validation errors, but none were present")
	}
	for _, finding := range findings {
		if finding.Reason != validation.DependencyUnavailable {
			return nil
		}
	}
	return fmt.Errorf("arctest: only dependencyUnavailable findings; seed the missing dependency or explicitly require that reason")
}

// RequireValidationReason requires at least one finding with the exact reason,
// avoiding message-text assertions and unrelated rejections passing a test.
func RequireValidationReason(tb testing.TB, findings []validation.Result, reason validation.Reason) {
	tb.Helper()
	if !hasReason(findings, reason) {
		tb.Fatalf("arctest: expected validation reason %q, got %+v", reason, findings)
	}
}

func hasReason(findings []validation.Result, reason validation.Reason) bool {
	for _, finding := range findings {
		if finding.Reason == reason {
			return true
		}
	}
	return false
}
