package validation_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cratis/arc.go/validation"
)

func TestInvokeCopiesAndPreservesRejection(t *testing.T) {
	detail := "detail"
	findings := []validation.Result{{Severity: validation.Error, Members: []string{"name"}, ReasonDetail: &detail}}
	rejected := validation.Reject(findings...)
	findings[0].Members[0] = "changed"
	detail = "changed"
	wrapped := fmt.Errorf("wrapped: %w", rejected)
	_, err := validation.Invoke(t.Context(), validation.ValidatorFunc[int](func(context.Context, int) ([]validation.Result, error) { return nil, wrapped }), 1)
	var failure validation.Failure
	if !errors.As(err, &failure) || !errors.Is(err, validation.ErrRejected) {
		t.Fatalf("error = %v", err)
	}
	got := failure.ValidationResults()
	got[0].Members[0] = "mutated"
	if failure.ValidationResults()[0].Members[0] != "name" || *failure.ValidationResults()[0].ReasonDetail != "detail" {
		t.Fatal("rejection aliased")
	}
	if validation.Reject() != nil {
		t.Fatal("empty rejection")
	}
	successful, err := validation.Invoke(t.Context(), validation.ValidatorFunc[int](func(context.Context, int) ([]validation.Result, error) { return findings, nil }), 1)
	if err != nil {
		t.Fatal(err)
	}
	successful[0].Members[0] = "copy"
	if findings[0].Members[0] != "changed" {
		t.Fatal("result aliased")
	}
}

func TestInvokeRedactsAndPreservesCancellation(t *testing.T) {
	secret := errors.New("credential secret")
	cases := []struct {
		name       string
		callback   validation.ValidatorFunc[int]
		cause      error
		panicValue any
	}{
		{"error", func(context.Context, int) ([]validation.Result, error) { return nil, secret }, secret, nil},
		{"panic", func(context.Context, int) ([]validation.Result, error) { panic("secret") }, nil, "secret"},
		{"severity", func(context.Context, int) ([]validation.Result, error) {
			return []validation.Result{{Severity: 9}}, nil
		}, validation.ErrInvalidSeverity, nil},
		{"rejected severity", func(context.Context, int) ([]validation.Result, error) {
			return nil, validation.Reject(validation.Result{Severity: -1})
		}, validation.ErrInvalidSeverity, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validation.Invoke(t.Context(), tc.callback, 1)
			var invocation *validation.InvocationError
			if !errors.As(err, &invocation) || !errors.Is(err, validation.ErrValidatorFailed) || (tc.cause != nil && !errors.Is(err, tc.cause)) || invocation.Panic != tc.panicValue {
				t.Fatalf("error = %#v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("leaked diagnostics")
			}
			want := []validation.Result{{Severity: validation.Error, Message: "The value could not be validated.", Members: []string{}, Reason: validation.ValidatorFailed}}
			if !reflect.DeepEqual(invocation.ValidationResults(), want) {
				t.Fatalf("findings = %#v", invocation.ValidationResults())
			}
		})
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		wrapped := fmt.Errorf("wrapped: %w", cause)
		_, err := validation.Invoke(t.Context(), validation.ValidatorFunc[int](func(context.Context, int) ([]validation.Result, error) { return nil, wrapped }), 0)
		if err != wrapped {
			t.Fatalf("cancellation = %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, err := validation.Invoke(ctx, validation.ValidatorFunc[int](func(context.Context, int) ([]validation.Result, error) { cancel(); return nil, nil }), 0)
	if err != context.Canceled {
		t.Fatal(err)
	}
	var callback validation.ValidatorFunc[int]
	if _, err := validation.Invoke(t.Context(), callback, 0); !errors.Is(err, validation.ErrInvalidValidator) {
		t.Fatal(err)
	}
}

func ExampleInvoke() {
	validator := validation.ValidatorFunc[int](func(_ context.Context, value int) ([]validation.Result, error) {
		if value < 0 {
			return []validation.Result{{Severity: validation.Error, Message: "Must be positive."}}, nil
		}
		return nil, nil
	})
	findings, err := validation.Invoke(context.Background(), validator, -1)
	fmt.Println(len(findings), err)
	// Output: 1 <nil>
}
