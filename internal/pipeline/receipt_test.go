package pipeline_test

import (
	"context"
	"testing"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/internal/pipeline"
)

func TestForwardedReceiptIsConsumedOnce(t *testing.T) {
	first := time.Unix(1, 0)
	second := time.Unix(2, 0)
	ctx, got, err := pipeline.Receipt(pipeline.ForwardReceipt(t.Context(), first), func() time.Time { t.Fatal("clock called"); return time.Time{} })
	if err != nil || got != first {
		t.Fatal(got, err)
	}
	ctx, got, err = pipeline.Receipt(ctx, func() time.Time { return second })
	if err != nil || got != second {
		t.Fatal(got, err)
	}
	_ = execution.WithReceivedAt(context.Background(), second)
}
