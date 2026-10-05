// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func startSSEServer(t *testing.T, b *arc.Builder, http2 bool) (*arc.Application, *httptest.Server) {
	t.Helper()
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(a)
	s.EnableHTTP2 = http2
	if http2 {
		s.StartTLS()
	} else {
		s.Start()
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		s.Close()
	})
	return a, s
}
func openSSE(t *testing.T, s *httptest.Server, method, path, body string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	r, err := http.NewRequestWithContext(ctx, method, s.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Accept", "application/json, TEXT/EVENT-STREAM")
	r.Header.Set(correlation.DefaultHeader, "12345678-1234-4234-8234-123456789012")
	response, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	})
	return response
}
func readSSEFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	blank, err := r.ReadString('\n')
	if err != nil || blank != "\n" {
		t.Fatalf("frame terminator = %q: %v", blank, err)
	}
	if !strings.HasPrefix(line, "data: ") {
		t.Fatalf("frame = %q", line)
	}
	return line + blank
}
func readSSEResult(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	frame := readSSEFrame(t, r)
	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(frame[6:])), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDirectSSERealListenerMatchesIndependentFixture(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState([]builderModel{}, observable.SubjectOptions[[]builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.RegisterObservable[builderModel](b, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[[]builderModel], error) {
		return state, nil
	}), queries.WithPath[queries.NoArguments]("/observe")); err != nil {
		t.Fatal(err)
	}
	_, server := startSSEServer(t, b, false)
	response := openSSE(t, server, "GET", "/observe", "")
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || response.Header.Get("Cache-Control") != "no-cache" || response.Header.Get("X-Accel-Buffering") != "no" || response.Header.Get("Connection") != "keep-alive" || response.Header.Get("Content-Length") != "" {
		t.Fatal(response.StatusCode, response.Header)
	}
	fixture, err := os.ReadFile("ContractTests/fixtures/v1/observable/direct.sse")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	if got := readSSEFrame(t, reader); got != string(fixture) {
		t.Fatalf("got %q, want %q", got, fixture)
	}
	if err := state.Complete(); err != nil {
		t.Fatal(err)
	}
	if tail, err := io.ReadAll(reader); err != nil || len(tail) != 0 {
		t.Fatalf("completion invented frame %q: %v", tail, err)
	}
}

func TestDirectSSEPendingFlushHTTP2AndQUERYNoStore(t *testing.T) {
	for _, method := range []string{"GET", "QUERY"} {
		t.Run(method, func(t *testing.T) {
			b, err := arc.NewBuilder(arc.Options{})
			if err != nil {
				t.Fatal(err)
			}
			state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{})
			if err != nil {
				t.Fatal(err)
			}
			registerAppObservation(t, b, state)
			_, server := startSSEServer(t, b, true)
			body := ""
			if method == "QUERY" {
				body = `{}`
			}
			// Headers must flush even without the first value or a wait timeout.
			response := openSSE(t, server, method, "/observe?waitForFirstResult=true", body)
			if response.ProtoMajor != 2 || response.StatusCode != 200 || response.Header.Get("Connection") != "" {
				t.Fatal(response.Proto, response.StatusCode, response.Header)
			}
			if method == "QUERY" && response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal(response.Header)
			}
			if err := state.Publish(t.Context(), builderModel{Name: "first"}); err != nil {
				t.Fatal(err)
			}
			result := readSSEResult(t, bufio.NewReader(response.Body))
			if result["isReady"] != true || result["data"].(map[string]any)["name"] != "first" {
				t.Fatal("synthetic pending or wrong result", result)
			}
		})
	}
}

func TestDirectSSEGuardSuppressesThenTerminatesWithoutFollowingData(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	registerAppObservation(t, b, state)
	guarded := make(chan queries.EmissionContext, 3)
	verdicts := []queries.EmissionVerdict{queries.Suppress, queries.Allow, queries.DenyAndTerminate}
	index := 0
	if err := b.Queries().AddEmissionGuard("test", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(_ context.Context, c queries.EmissionContext) (queries.EmissionVerdict, error) {
			guarded <- c
			v := verdicts[index]
			index++
			return v, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	_, server := startSSEServer(t, b, false)
	response := openSSE(t, server, "GET", "/observe", "")
	reader := bufio.NewReader(response.Body)
	for _, value := range []string{"suppressed", "allowed", "denied", "never"} {
		if err := state.Publish(t.Context(), builderModel{Name: value}); err != nil {
			t.Fatal(err)
		}
	}
	allowed := readSSEResult(t, reader)
	denied := readSSEResult(t, reader)
	if allowed["data"].(map[string]any)["name"] != "allowed" || denied["isAuthorized"] != false || denied["data"] != nil {
		t.Fatal(allowed, denied)
	}
	if tail, err := io.ReadAll(reader); err != nil || len(tail) != 0 {
		t.Fatalf("post-terminal bytes %q: %v", tail, err)
	}
	first := []bool{(<-guarded).FirstDelivered(), (<-guarded).FirstDelivered(), (<-guarded).FirstDelivered()}
	if !first[0] || !first[1] || first[2] {
		t.Fatal("suppression advanced first delivery", first)
	}
}

func TestDirectSSEPreAdmissionDenialDoesNotOpenSource(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := queries.RegisterObservable[builderModel](b, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[builderModel], error) {
		calls++
		return nil, errors.New("must not open")
	}), queries.WithPath[queries.NoArguments]("/observe"), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{})); err != nil {
		t.Fatal(err)
	}
	_, server := startSSEServer(t, b, false)
	response := openSSE(t, server, "GET", "/observe", "")
	if response.StatusCode != 403 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") || calls != 0 {
		t.Fatal(response.StatusCode, response.Header, calls)
	}
}

func TestDirectSSEDisconnectDoesNotFailApplicationOwnedSubject(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState(builderModel{Name: "alive"}, observable.SubjectOptions[builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	registerAppObservation(t, b, state)
	a, server := startSSEServer(t, b, false)
	response := openSSE(t, server, "GET", "/observe", "")
	readSSEResult(t, bufio.NewReader(response.Body))
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := state.Publish(t.Context(), builderModel{Name: "still alive"}); err != nil {
		t.Fatal("transport failed shared subject", err)
	}
}

func TestDirectSSEReplacesCacheablePolicyFromApplicationMiddleware(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, max-age=600")
			next.ServeHTTP(w, r)
		})
	}); err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState([]builderModel{}, observable.SubjectOptions[[]builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.RegisterObservable[builderModel](b, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[[]builderModel], error) {
		return state, nil
	}), queries.WithPath[queries.NoArguments]("/observe")); err != nil {
		t.Fatal(err)
	}
	_, server := startSSEServer(t, b, false)
	response := openSSE(t, server, "GET", "/observe", "")
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-cache" {
		t.Fatal(response.StatusCode, response.Header)
	}
}
