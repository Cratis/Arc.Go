package arc_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

type unencodable struct{}

func (unencodable) MarshalJSON() ([]byte, error) { panic("SECRET codec") }
func TestAtomicPublicationFailuresPreserveStatusPrecedence(t *testing.T) {
	for _, status := range []int{500, 400, 403} {
		b, err := arc.NewBuilder(arc.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := commands.Register[builderCommand](b, commands.Invoke(func(ctx context.Context, _ *commands.Invocation, _ builderCommand) (commands.Outcome[any], error) {
			snapshot, _ := commands.ContextFrom(ctx)
			d := commands.Details{CorrelationID: snapshot.CorrelationID(), Authorized: status != 403}
			if status == 400 || status == 403 {
				d.ValidationResults = []validation.Result{{Severity: validation.Error, Message: "safe", State: make(chan int)}}
				return commands.Control[any](commands.NewResult(d, serialization.Optional[commands.NoResponse]{})), nil
			}
			return commands.Respond[any](unencodable{}), nil
		}), commands.WithPath[builderCommand]("/publish")); err != nil {
			t.Fatal(err)
		}
		a, err := buildStarted(t, b)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", "/publish", strings.NewReader("{}")))
		if w.Code != status || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), `"response"`) || !strings.Contains(w.Body.String(), "An internal error occurred while processing the request. See server logs for details.") {
			t.Fatal(status, w.Code, w.Body.String())
		}
	}
}
func TestHTTPReceiptPrecedesAuthenticationAndNestedReceiptIsFresh(t *testing.T) {
	tick := int64(0)
	clock := func() time.Time { tick++; return time.Unix(tick, 0) }
	var receipts []time.Time
	b, err := arc.NewBuilder(arc.Options{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[httpCommand](b, commands.Invoke(func(ctx context.Context, _ *commands.Invocation, _ httpCommand) (int, error) {
		received, _ := execution.ReceivedAt(ctx)
		receipts = append(receipts, received)
		return 1, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[builderCommand](b, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ builderCommand) (int, error) {
		received, _ := execution.ReceivedAt(ctx)
		receipts = append(receipts, received)
		_, err := inv.Pipeline().Execute(ctx, httpCommand{Name: "nested"})
		return 2, err
	}), commands.WithPath[builderCommand]("/receipt")); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/receipt", strings.NewReader("{}")))
	if w.Code != 200 || len(receipts) != 2 || !receipts[0].Equal(time.Unix(1, 0)) || !receipts[1].Equal(time.Unix(2, 0)) {
		t.Fatal(w.Code, receipts, w.Body.String())
	}
}
