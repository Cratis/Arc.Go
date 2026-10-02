package authentication_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/identity"
)

func TestTerminalOrderAndCopies(t *testing.T) {
	success, err := authentication.Authenticated(identity.System())
	if err != nil {
		t.Fatal(err)
	}
	for _, terminal := range []authentication.Result{success, authentication.Failed("secret")} {
		calls := 0
		handlers := []authentication.Handler{
			authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
				calls++
				return authentication.Anonymous(), nil
			}),
			authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) { calls++; return terminal, nil }),
			authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
				t.Fatal("terminal rescued")
				return success, nil
			}),
		}
		chain, err := authentication.New(handlers...)
		if err != nil {
			t.Fatal(err)
		}
		handlers[1] = handlers[2]
		got, err := chain.Authenticate(t.Context(), httptest.NewRequest("GET", "/", nil))
		if err != nil || calls != 2 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		_, ok := got.Principal()
		_, want := terminal.Principal()
		if ok != want {
			t.Fatal("terminal changed")
		}
		if failure := got.Failure(); failure != nil && (!errors.Is(failure, authentication.ErrFailed) || failure.Reason() != "secret" || failure.Error() == "secret") {
			t.Fatal("failure semantics")
		}
	}
	var empty authentication.Chain
	result, err := empty.Authenticate(t.Context(), httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Principal(); ok || result.Failure() != nil {
		t.Fatal("empty chain")
	}
}

func TestInvalidAndCancellation(t *testing.T) {
	var nilFunc authentication.HandlerFunc
	for _, handler := range []authentication.Handler{nil, nilFunc} {
		if _, err := authentication.New(handler); !errors.Is(err, authentication.ErrInvalidHandler) {
			t.Fatal(err)
		}
	}
	if _, err := authentication.Authenticated(identity.Principal{}); !errors.Is(err, authentication.ErrInvalidPrincipal) {
		t.Fatal(err)
	}
	boom := errors.New("ordinary")
	chain, err := authentication.New(authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
		return authentication.Result{}, boom
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chain.Authenticate(t.Context(), nil); !errors.Is(err, authentication.ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := chain.Authenticate(t.Context(), httptest.NewRequest("GET", "/", nil)); err != boom {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	chain, err = authentication.New(authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
		cancel()
		return authentication.Failed("rejected"), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chain.Authenticate(ctx, httptest.NewRequest("GET", "/", nil)); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestTrustedHostAndConcurrentContexts(t *testing.T) {
	chain, err := authentication.New(authentication.HostPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("x-ms-client-principal", "spoofed")
	request.Header.Set("Cookie", ".cratis-identity=spoofed")
	request.SetBasicAuth("administrator", "password")
	result, err := chain.Authenticate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Principal(); ok {
		t.Fatal("spoofed authentication")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			principal := identity.NewPrincipal(identity.PrincipalData{ID: fmt.Sprint(i), AuthenticationType: "verified"})
			ctx := identity.WithPrincipal(t.Context(), principal)
			got, err := chain.Authenticate(ctx, request)
			if err != nil {
				t.Error(err)
				return
			}
			actor, ok := got.Principal()
			if !ok || !actor.Equal(principal) {
				t.Error("cross-request identity")
			}
		})
	}
	wg.Wait()
	if _, ok := identity.PrincipalFrom(request.Context()); ok {
		t.Fatal("incoming context mutated")
	}
	check, err := authentication.New(authentication.HandlerFunc(func(ctx context.Context, r *http.Request) (authentication.Result, error) {
		if r == request || r.Context() != ctx {
			t.Error("callback context mismatch")
		}
		return authentication.Anonymous(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := check.Authenticate(t.Context(), request); err != nil {
		t.Fatal(err)
	}
}

func ExampleHostPrincipal() {
	chain, err := authentication.New(authentication.HostPrincipal())
	if err != nil {
		panic(err)
	}
	ctx := identity.WithPrincipal(context.Background(), identity.System("jobs"))
	result, err := chain.Authenticate(ctx, httptest.NewRequest("GET", "/", nil))
	if err != nil {
		panic(err)
	}
	actor, ok := result.Principal()
	fmt.Println(actor.Name(), ok)
	// Output: [System] true
}
