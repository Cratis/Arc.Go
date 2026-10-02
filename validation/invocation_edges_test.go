package validation_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cratis/arc.go/validation"
)

func TestInvokePreservesCancellationWrapperWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	original := fmt.Errorf("callback cancellation: %w", context.Canceled)
	_, err := validation.Invoke(ctx, validation.ValidatorFunc[int](func(context.Context, int) ([]validation.Result, error) { cancel(); return nil, original }), 0)
	if err != original {
		t.Fatalf("error=%v want original wrapper", err)
	}
}

func TestInvokeRejectsInvalidFindingsAlongsideFailure(t *testing.T) {
	_, err := validation.Invoke(t.Context(), validation.ValidatorFunc[int](func(context.Context, int) ([]validation.Result, error) {
		return []validation.Result{{Severity: 99}}, validation.Reject(validation.Result{Severity: validation.Error})
	}), 0)
	if !errors.Is(err, validation.ErrValidatorFailed) || !errors.Is(err, validation.ErrInvalidSeverity) {
		t.Fatalf("error=%v", err)
	}
}
