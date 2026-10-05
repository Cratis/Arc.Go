// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/validation"
)

func TestActionHandlerRejectsUnsupportedSeveritiesUnderEveryProfile(t *testing.T) {
	profiles := []struct {
		name   string
		strict bool
		ignore string
	}{{"default", false, ""}, {"warnings block", true, ""}, {"warnings ignored", true, "true"}}
	for _, profile := range profiles {
		for _, severity := range []validation.Severity{-1, 4, 255} {
			for _, source := range []string{"validator", "action result"} {
				t.Run(profile.name+"/"+source, func(t *testing.T) {
					actions, raw := 0, 0
					finding := []validation.Result{{Severity: severity, Message: "secret-finding"}}
					options := arc.ActionOptions[actionInput]{TreatWarningsAsErrors: profile.strict}
					if source == "validator" {
						options.Validate = func(context.Context, actionInput) ([]validation.Result, error) { return finding, nil }
					}
					handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
						actions++
						result := arc.ActionResult{Response: "secret-response"}
						if source == "action result" {
							result.ValidationResults = finding
							result.Response = nil
							result.Raw = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { raw++ })
						}
						return result, nil
					}, options)
					if err != nil {
						t.Fatal(err)
					}
					r := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(`{}`))
					r.Header.Set("X-Ignore-Warnings", profile.ignore)
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					var result actionEnvelope
					if source == "validator" && actions != 0 || raw != 0 || w.Code < 400 || strings.Contains(w.Body.String(), "secret") {
						t.Fatalf("severity %d failed open: actions=%d raw=%d code=%d body=%s", severity, actions, raw, w.Code, w.Body)
					}
					_, result = actionHTTP(t, handler, "/action", `{}`)
					if result.IsSuccess || len(result.Response) != 0 && string(result.Response) != "null" {
						t.Fatalf("severity %d produced success: %+v", severity, result)
					}
				})
			}
		}
	}
}

func TestActionHandlerValidSeveritiesKeepPolicyPrecedence(t *testing.T) {
	handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
		return arc.ActionResult{Response: "ok"}, nil
	}, arc.ActionOptions[actionInput]{Validate: func(context.Context, actionInput) ([]validation.Result, error) {
		return []validation.Result{{Severity: validation.Information, Message: "info"}, {Severity: validation.Warning, Message: "warn"}, {Severity: validation.Error, Message: "rejected"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	w, result := actionHTTP(t, handler, "/action", `{}`)
	if w.Code != http.StatusBadRequest || len(result.ValidationResults) != 1 || result.ValidationResults[0].Message != "rejected" {
		t.Fatalf("code=%d result=%+v", w.Code, result)
	}
}

func TestActionHandlerFiltersFindingsReturnedWithInfrastructureError(t *testing.T) {
	for _, strict := range []bool{false, true} {
		handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
			t.Fatal("action executed")
			return arc.ActionResult{}, nil
		}, arc.ActionOptions[actionInput]{TreatWarningsAsErrors: strict, Validate: func(context.Context, actionInput) ([]validation.Result, error) {
			return []validation.Result{{Severity: validation.Warning, Message: "warn"}}, errors.New("secret-infrastructure")
		}})
		if err != nil {
			t.Fatal(err)
		}
		w, result := actionHTTP(t, handler, "/action", `{}`)
		// Infrastructure failure is a 500 when the warning is filtered away; once
		// the policy retains the warning it is a validation response.
		want, findings := http.StatusInternalServerError, 0
		if strict {
			want, findings = http.StatusBadRequest, 1
		}
		if w.Code != want || len(result.ValidationResults) != findings || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("strict=%v code=%d body=%s", strict, w.Code, w.Body)
		}
	}
}
