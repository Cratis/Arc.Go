package arc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/queries"
)

type receiptReader struct{ check func(context.Context) }

func (receiptReader) Method() string               { return "GET" }
func (receiptReader) ResponseCacheControl() string { return "" }
func (r receiptReader) Read(ctx context.Context, in queries.ReaderInput) (queries.Request, error) {
	r.check(ctx)
	return queries.ReadGET(in.Query)
}

func TestHTTPReceiptForwardingIsPrivateToEndpointPipeline(t *testing.T) {
	var tick int64
	clock := func() time.Time { tick++; return time.Unix(tick, 0) }
	check := func(ctx context.Context) {
		t.Helper()
		_, received, err := boundary.Receipt(ctx, func() time.Time { return time.Unix(999, 0) })
		if err != nil || !received.Equal(time.Unix(999, 0)) {
			t.Fatal("forwarding marker escaped endpoint", received, err)
		}
	}
	b, err := arc.NewBuilder(arc.Options{Environment: "Development", Clock: clock, Authentication: []authentication.Handler{authentication.HostPrincipal()}, HTTP: arc.HTTPOptions{QueryReaders: []queries.RequestReader{receiptReader{check}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { check(r.Context()); next.ServeHTTP(w, r) })
	}); err != nil {
		t.Fatal(err)
	}
	var receipts []time.Time
	if err := commands.Register[builderCommand](b, commands.Void(func(_ builderCommand, ctx context.Context) error {
		received, _ := execution.ReceivedAt(ctx)
		receipts = append(receipts, received)
		return nil
	}), commands.WithPath[builderCommand]("/command")); err != nil {
		t.Fatal(err)
	}
	if err := queries.Register[builderModel](b, "All", queries.Function(func(ctx context.Context, _ queries.NoArguments) ([]builderModel, error) {
		received, _ := execution.ReceivedAt(ctx)
		receipts = append(receipts, received)
		return nil, nil
	}), queries.WithPath[queries.NoArguments]("/query")); err != nil {
		t.Fatal(err)
	}
	if err := arc.RegisterIdentityDetails(b, "receipt", identity.DetailsProviderFunc[struct{}](func(ctx context.Context, _ identity.Context) (identity.Details[struct{}], error) {
		check(ctx)
		return identity.Details[struct{}]{IsUserAuthorized: true}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := b.AddUsersProvider("receipt", func(ctx context.Context, _ *execution.Scope) (arc.UsersProvider, error) {
		check(ctx)
		return userList{"test"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	var a *arc.Application
	if err := b.Handle("/raw", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		check(r.Context())
		for range 2 {
			if _, err := a.Commands().Execute(r.Context(), builderCommand{}); err != nil {
				t.Fatal(err)
			}
		}
		w.WriteHeader(200)
	})); err != nil {
		t.Fatal(err)
	}
	a, err = buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []struct{ method, path string }{{"POST", "/command"}, {"POST", "/command/validate"}, {"GET", "/query"}, {"GET", "/.cratis/me"}, {"GET", "/.cratis/users"}, {"GET", "/raw"}} {
		before := tick
		w := httptest.NewRecorder()
		r := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader("{}"))
		r = r.WithContext(identity.WithPrincipal(t.Context(), identity.System()))
		a.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(endpoint, w.Code, w.Body.String())
		}
		if endpoint.path == "/command" || endpoint.path == "/query" {
			if !receipts[len(receipts)-1].Equal(time.Unix(before+1, 0)) {
				t.Fatal("endpoint lost HTTP receipt", receipts)
			}
		}
	}
	if len(receipts) != 4 || receipts[2].Equal(receipts[3]) || !receipts[2].Equal(time.Unix(tick-1, 0)) || !receipts[3].Equal(time.Unix(tick, 0)) {
		t.Fatal("raw handler reused the HTTP receipt", receipts)
	}
}
