package arc_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/queries"
)

func TestIngressRecoversMiddlewareAndRawPanics(t *testing.T) {
	for _, logger := range []bool{false, true} {
		for _, endpoint := range []struct {
			method, path string
			envelope     bool
		}{{"POST", "/command", true}, {"GET", "/query", true}, {"HEAD", "/query", false}, {"GET", "/.cratis/me", false}, {"GET", "/raw", false}} {
			t.Run(endpoint.method+endpoint.path+map[bool]string{false: " without logger", true: " with logger"}[logger], func(t *testing.T) {
				var logs bytes.Buffer
				o := arc.Options{}
				if logger {
					o.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
				}
				b, err := arc.NewBuilder(o)
				if err != nil {
					t.Fatal(err)
				}
				if err := commands.Register[builderCommand](b, commands.WithPath[builderCommand]("/command")); err != nil {
					t.Fatal(err)
				}
				if err := queries.Register[builderModel](b, "All", queries.Function(func(context.Context, queries.NoArguments) ([]builderModel, error) { return nil, nil }), queries.WithPath[queries.NoArguments]("/query")); err != nil {
					t.Fatal(err)
				}
				if err := b.Handle("/raw", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("SECRET raw") })); err != nil {
					t.Fatal(err)
				}
				if err := b.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/raw" {
							next.ServeHTTP(w, r)
							return
						}
						w.Header().Set("Content-Length", "9999")
						panic("SECRET middleware")
					})
				}); err != nil {
					t.Fatal(err)
				}
				a, err := buildStarted(t, b)
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				a.ServeHTTP(w, httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader("{}")))
				if w.Code != 500 || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(logs.String(), "SECRET") {
					t.Fatal(w.Code, w.Body.String(), logs.String())
				}
				if endpoint.envelope != (w.Body.Len() > 0) {
					t.Fatal("unexpected envelope", w.Body.String())
				}
				if logger && !strings.Contains(logs.String(), `"level":"ERROR"`) {
					t.Fatal(logs.String())
				}
				if err := a.Shutdown(t.Context()); err != nil {
					t.Fatal("admission leaked", err)
				}
			})
		}
	}
}

func TestIngressDoesNotRewriteCommittedResponsesAndPropagatesAbort(t *testing.T) {
	for _, abort := range []bool{false, true} {
		var logs bytes.Buffer
		b, err := arc.NewBuilder(arc.Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Handle("/panic", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if abort {
				panic(http.ErrAbortHandler)
			}
			w.WriteHeader(202)
			if _, err := w.Write([]byte("published")); err != nil {
				t.Fatal(err)
			}
			panic("SECRET")
		})); err != nil {
			t.Fatal(err)
		}
		a, err := buildStarted(t, b)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			a.ServeHTTP(w, httptest.NewRequest("GET", "/panic", nil))
		}()
		if abort {
			if recovered != http.ErrAbortHandler || logs.Len() != 0 {
				t.Fatal(recovered, logs.String())
			}
		} else if recovered != nil || w.Code != 202 || w.Body.String() != "published" || !strings.Contains(logs.String(), `"level":"ERROR"`) {
			t.Fatal(recovered, w.Code, w.Body.String(), logs.String())
		}
		if err := a.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}
