package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
)

func TestModelBoundGreeting(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[Greet](b, commands.Handle(Greet.Handle)); err != nil {
		t.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/api/greet", strings.NewReader(`{"name":"Ada"}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"response":"Hello, Ada"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
