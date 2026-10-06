// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func TestDirectSSESourceFailureSendsSafeTerminalResult(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	source := observable.FromProducer(func(context.Context, func(builderModel) error) error { return errors.New("secret provider diagnostic") })
	registerAppObservation(t, b, source)
	_, server := startSSEServer(t, b, false)
	response := openSSE(t, server, "GET", "/observe", "")
	reader := bufio.NewReader(response.Body)
	result := readSSEResult(t, reader)
	if result["hasExceptions"] != true || result["isSuccess"] != false || result["data"] != nil || strings.Contains(result["exceptionMessages"].([]any)[0].(string), "secret") {
		t.Fatal(result)
	}
	if tail, err := io.ReadAll(reader); err != nil || len(tail) != 0 {
		t.Fatal(string(tail), err)
	}
}

func TestDirectSSEOversizedCandidateSendsOnlySafeFailure(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{HTTP: arc.HTTPOptions{MaxResponseBytes: 512}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState(builderModel{Name: strings.Repeat("private", 256)}, observable.SubjectOptions[builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	registerAppObservation(t, b, state)
	_, server := startSSEServer(t, b, false)
	response := openSSE(t, server, "GET", "/observe", "")
	reader := bufio.NewReader(response.Body)
	result := readSSEResult(t, reader)
	if result["hasExceptions"] != true || result["data"] != nil {
		t.Fatal(result)
	}
	if tail, err := io.ReadAll(reader); err != nil || len(tail) != 0 {
		t.Fatal(string(tail), err)
	}
	if err := state.Publish(t.Context(), builderModel{Name: "still owned"}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectSSEEnumerableSkipsNullButSubjectForwardsNull(t *testing.T) {
	for _, enumerable := range []bool{false, true} {
		t.Run(map[bool]string{false: "subject", true: "enumerable"}[enumerable], func(t *testing.T) {
			b, err := arc.NewBuilder(arc.Options{})
			if err != nil {
				t.Fatal(err)
			}
			source := observable.FromProducer(func(ctx context.Context, emit func(*builderModel) error) error {
				if err := emit(nil); err != nil {
					return err
				}
				return emit(&builderModel{Name: "value"})
			})
			options := []queries.Option[queries.NoArguments]{queries.WithPath[queries.NoArguments]("/observe")}
			if enumerable {
				options = append(options, queries.WithEnumerable[queries.NoArguments]())
			}
			if err := queries.RegisterObservable[builderModel](b, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[*builderModel], error) {
				return source, nil
			}), options...); err != nil {
				t.Fatal(err)
			}
			_, server := startSSEServer(t, b, false)
			response := openSSE(t, server, "GET", "/observe", "")
			reader := bufio.NewReader(response.Body)
			if !enumerable {
				result := readSSEResult(t, reader)
				if result["isReady"] != true || result["isSuccess"] != true || result["data"] != nil {
					t.Fatal(result)
				}
			}
			result := readSSEResult(t, reader)
			if result["data"].(map[string]any)["name"] != "value" {
				t.Fatal(result)
			}
			if tail, err := io.ReadAll(reader); err != nil || len(tail) != 0 {
				t.Fatal(string(tail), err)
			}
		})
	}
}
