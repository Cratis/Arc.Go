// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/validation"
)

type actionInput struct {
	Count int  `json:"count"`
	Flag  bool `json:"flag"`
}

type actionEnvelope struct {
	CorrelationID     string              `json:"correlationId"`
	IsSuccess         bool                `json:"isSuccess"`
	IsAuthorized      bool                `json:"isAuthorized"`
	IsValid           bool                `json:"isValid"`
	HasExceptions     bool                `json:"hasExceptions"`
	ValidationResults []validation.Result `json:"validationResults"`
	ExceptionMessages []string            `json:"exceptionMessages"`
	Response          json.RawMessage     `json:"response"`
}

func actionHTTP(t *testing.T, handler http.Handler, path, body string) (*httptest.ResponseRecorder, actionEnvelope) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var result actionEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid envelope %q: %v", response.Body.String(), err)
	}
	return response, result
}

// Authority for action/validate/failed-response behavior: Arc
// 7c1e78075b737df64f69fddfaae83374f75e3612, Commands/CommandActionFilter.cs and
// Arc.Specs/Commands/for_CommandActionFilter/when_executing. This is a native
// source-derived contract witness, not paired execution of MVC's filters.
func TestActionHandlerValidationNeverInvokesAction(t *testing.T) {
	for _, path := range []string{"/action/validate", "/action/VALIDATE"} {
		for _, invalid := range []bool{false, true} {
			actions, validations := 0, 0
			handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
				actions++
				return arc.ActionResult{Response: "forbidden"}, nil
			}, arc.ActionOptions[actionInput]{Validate: func(context.Context, actionInput) ([]validation.Result, error) {
				validations++
				if invalid {
					return []validation.Result{{Severity: validation.Error, Message: "invalid"}}, nil
				}
				return nil, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			_, result := actionHTTP(t, handler, path, `{}`)
			if actions != 0 || validations != 1 || result.IsSuccess == invalid || len(result.Response) != 0 {
				t.Fatalf("actions=%d validations=%d result=%+v", actions, validations, result)
			}
		}
	}
}

func TestActionHandlerRejectsBeforeAction(t *testing.T) {
	calls := 0
	handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
		calls++
		return arc.ActionResult{}, nil
	}, arc.ActionOptions[actionInput]{Validate: func(context.Context, actionInput) ([]validation.Result, error) {
		return []validation.Result{{Severity: validation.Error, Message: "rejected"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	w, result := actionHTTP(t, handler, "/action", `{}`)
	if calls != 0 || w.Code != 400 || result.IsValid || result.IsSuccess {
		t.Fatalf("calls=%d code=%d result=%+v", calls, w.Code, result)
	}
}

func TestActionHandlerSuppressesFailedResponsesAndRedacts(t *testing.T) {
	for _, mode := range []string{"validation", "validation-raw", "exception", "panic", "denied", "encoding", "ambiguous-raw"} {
		t.Run(mode, func(t *testing.T) {
			rawCalls := 0
			handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
				result := arc.ActionResult{Response: "secret-response"}
				switch mode {
				case "validation", "validation-raw":
					result.ValidationResults = []validation.Result{{Severity: validation.Error, Message: "rejected"}}
					if mode == "validation-raw" {
						result.Response = nil
						result.Raw = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { rawCalls++ })
					}
				case "ambiguous-raw":
					result.Raw = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { rawCalls++ })
				case "exception":
					return result, errors.New("secret-error")
				case "panic":
					panic("secret-panic")
				case "denied":
					return result, authorization.ErrDenied
				case "encoding":
					result.Response = make(chan int)
				}
				return result, nil
			}, arc.ActionOptions[actionInput]{})
			if err != nil {
				t.Fatal(err)
			}
			w, result := actionHTTP(t, handler, "/action", `{}`)
			wantStatus := 500
			if strings.HasPrefix(mode, "validation") {
				wantStatus = 400
			}
			if mode == "denied" {
				wantStatus = 403
			}
			if w.Code != wantStatus || result.IsSuccess || len(result.Response) != 0 || strings.Contains(w.Body.String(), "secret") || rawCalls != 0 {
				t.Fatalf("raw=%d code=%d body=%s", rawCalls, w.Code, w.Body)
			}
		})
	}
}

func TestActionHandlerRetainsZeroFalseAndNullResponseContract(t *testing.T) {
	for _, response := range []any{0, false, nil} {
		handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
			return arc.ActionResult{Response: response}, nil
		}, arc.ActionOptions[actionInput]{})
		if err != nil {
			t.Fatal(err)
		}
		w, result := actionHTTP(t, handler, "/action", `{}`)
		want := fmt.Sprint(response)
		if response == nil {
			want = ""
		}
		if w.Code != 200 || !result.IsSuccess || string(result.Response) != want || result.ExceptionMessages == nil || result.ValidationResults == nil {
			t.Fatalf("unexpected envelope: %s", w.Body)
		}
	}
}

func TestActionHandlerRawOptOutRemainsRaw(t *testing.T) {
	handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
		return arc.ActionResult{Raw: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Location", "/created")
			w.WriteHeader(http.StatusCreated)
			if _, err := w.Write([]byte("raw-result")); err != nil {
				t.Error(err)
			}
		})}, nil
	}, arc.ActionOptions[actionInput]{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(`{}`)))
	if w.Code != 201 || w.Body.String() != "raw-result" || w.Header().Get("Location") != "/created" || w.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("unexpected raw response: %v %q", w.Result(), w.Body.String())
	}
}

func TestActionHandlerInputLimitsAndMedia(t *testing.T) {
	cases := []struct {
		name, path, body, media string
		status                  int
	}{
		{"malformed", "/action", `{`, "application/json", 400},
		{"trailing", "/action", `{} {}`, "application/json", 400},
		{"oversized", "/action", strings.Repeat(" ", 65), "application/json", 413},
		{"media", "/action", `{}`, "text/plain", 415},
		{"query", "/action?name=too-long", `{}`, "application/json", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
				calls++
				return arc.ActionResult{}, nil
			}, arc.ActionOptions[actionInput]{MaxBodyBytes: 64, MaxQueryBytes: 8})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.media)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if calls != 0 || w.Code != tc.status || strings.Contains(w.Body.String(), `"response":`) {
				t.Fatalf("calls=%d code=%d body=%s", calls, w.Code, w.Body)
			}
		})
	}
}

func TestActionHandlerWarningsAndCancellation(t *testing.T) {
	for _, ignore := range []string{"", "true", "TRUE", "1"} {
		calls := 0
		handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
			calls++
			return arc.ActionResult{}, nil
		}, arc.ActionOptions[actionInput]{TreatWarningsAsErrors: true, Validate: func(context.Context, actionInput) ([]validation.Result, error) {
			return []validation.Result{{Severity: validation.Warning, Message: "warning"}}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(`{}`))
		r.Header.Set("X-Ignore-Warnings", ignore)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 0
		if strings.EqualFold(ignore, "true") {
			want = 1
		}
		if calls != want {
			t.Fatalf("header=%s calls=%d want=%d", ignore, calls, want)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r = httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(`{}`)).WithContext(ctx)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if calls != want || w.Code != 500 {
			t.Fatalf("canceled action ran or succeeded: calls=%d status=%d", calls, w.Code)
		}
	}
}

func TestActionHandlerExplicitRequestMergeAndContext(t *testing.T) {
	id, err := concepts.ParseUUID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := arc.NewActionHandler(func(ctx context.Context, input actionInput) (arc.ActionResult, error) {
		if correlation.FromContext(ctx) != id {
			t.Error("lost request context")
		}
		return arc.ActionResult{Response: input}, nil
	}, arc.ActionOptions[actionInput]{FromRequest: func(r *http.Request) (actionInput, error) {
		count, err := strconv.Atoi(r.URL.Query().Get("count"))
		if err != nil {
			return actionInput{}, &commands.DecodeError{Cause: err}
		}
		return actionInput{Count: count, Flag: true}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/action?count=42", strings.NewReader(`{"count":0,"flag":false}`))
	request = request.WithContext(correlation.WithID(request.Context(), id))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	var result actionEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.CorrelationID != id.String() || string(result.Response) != `{"count":42,"flag":true}` {
		t.Fatalf("unexpected merged result: %s", w.Body)
	}
}

func TestActionHandlerRejectsInvalidConstructionAndMethods(t *testing.T) {
	action := func(context.Context, actionInput) (arc.ActionResult, error) { return arc.ActionResult{}, nil }
	if _, err := arc.NewActionHandler[actionInput](nil, arc.ActionOptions[actionInput]{}); err == nil {
		t.Fatal("accepted nil action")
	}
	if _, err := arc.NewActionHandler(action, arc.ActionOptions[actionInput]{MaxBodyBytes: -1}); err == nil {
		t.Fatal("accepted negative limit")
	}
	handler, err := arc.NewActionHandler(action, arc.ActionOptions[actionInput]{})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD", "DELETE"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/action", nil))
		if w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Fatalf("method %s accepted", method)
		}
	}
}

func TestActionHandlerCallbackFailuresRemainSafe(t *testing.T) {
	for _, mode := range []string{"request-error", "request-panic", "request-malformed", "validator-error", "validator-panic"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
				calls++
				return arc.ActionResult{}, nil
			}, arc.ActionOptions[actionInput]{
				FromRequest: func(*http.Request) (actionInput, error) {
					switch mode {
					case "request-error":
						return actionInput{}, errors.New("secret-request")
					case "request-panic":
						panic("secret-request")
					case "request-malformed":
						return actionInput{}, &commands.DecodeError{Cause: errors.New("secret-input")}
					}
					return actionInput{}, nil
				},
				Validate: func(context.Context, actionInput) ([]validation.Result, error) {
					if mode == "validator-panic" {
						panic("secret-validator")
					}
					return nil, errors.New("secret-validator")
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			w, result := actionHTTP(t, handler, "/action", `{}`)
			want := 500
			if mode == "request-malformed" {
				want = 400
			}
			if calls != 0 || w.Code != want || result.IsSuccess || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("calls=%d status=%d body=%s", calls, w.Code, w.Body)
			}
		})
	}
}

func TestActionHandlerResponseLimitDoesNotPublishSuccess(t *testing.T) {
	handler, err := arc.NewActionHandler(func(context.Context, actionInput) (arc.ActionResult, error) {
		return arc.ActionResult{Response: strings.Repeat("secret", 500)}, nil
	}, arc.ActionOptions[actionInput]{MaxResponseBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	w, result := actionHTTP(t, handler, "/action", `{}`)
	if w.Code != 500 || !result.HasExceptions || result.IsSuccess || len(result.Response) != 0 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("unexpected oversized publication: %d %s", w.Code, w.Body)
	}
}

func ExampleNewActionHandler() {
	type input struct{ Name string }
	handler, err := arc.NewActionHandler(func(_ context.Context, value input) (arc.ActionResult, error) {
		return arc.ActionResult{Response: "Hello, " + value.Name}, nil
	}, arc.ActionOptions[input]{})
	if err != nil {
		fmt.Println(err)
		return
	}
	builder, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, route := range []string{"POST /greet", "POST /greet/validate"} {
		if err := builder.Handle(route, handler); err != nil {
			fmt.Println(err)
			return
		}
	}
	app, err := builder.Build()
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := app.Start(context.Background()); err != nil {
		fmt.Println(err)
		return
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("POST", "/greet", strings.NewReader(`{"name":"Ada"}`)))
	var result actionEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(response.Code, string(result.Response))
	if err := app.Shutdown(context.Background()); err != nil {
		fmt.Println(err)
	}
	// Output: 200 "Hello, Ada"
}
