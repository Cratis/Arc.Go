// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

func TestStatusPrecedenceAndFailedResponseOmission(t *testing.T) {
	for _, authorized := range []bool{false, true} {
		for _, valid := range []bool{false, true} {
			for _, exceptions := range []bool{false, true} {
				details := commands.Details{Authorized: authorized}
				if !valid {
					details.ValidationResults = []validation.Result{{Severity: validation.Information}}
				}
				if exceptions {
					details.ExceptionMessages = []string{"error"}
				}
				result := commands.NewResult(details, serialization.Some(0))
				want := 200
				if !authorized {
					want = 403
				} else if !valid {
					want = 400
				} else if exceptions {
					want = 500
				}
				if result.StatusCode() != want {
					t.Errorf("status = %d, want %d", result.StatusCode(), want)
				}
				_, present := result.Response()
				if present != (want == 200) {
					t.Fatal("failed response remains present")
				}
				data, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), `"response"`) != (want == 200) {
					t.Fatalf("response wire presence = %s", data)
				}
			}
		}
	}
}

func TestResultCopiesFrameworkOwnedState(t *testing.T) {
	reason := "original"
	d := commands.Details{Authorized: true, ValidationResults: []validation.Result{{Members: []string{"name"}, ReasonDetail: &reason}}, ExceptionMessages: []string{"safe"}}
	result := commands.NewResult(d, serialization.Optional[int]{})
	d.ValidationResults[0].Members[0] = "changed"
	d.ExceptionMessages[0] = "changed"
	reason = "changed"
	copy := result.Details()
	copy.ValidationResults[0].Members[0] = "also changed"
	*copy.ValidationResults[0].ReasonDetail = "also changed"
	copy.ExceptionMessages[0] = "also changed"
	got := result.Details()
	if got.ValidationResults[0].Members[0] != "name" || *got.ValidationResults[0].ReasonDetail != "original" || got.ExceptionMessages[0] != "safe" {
		t.Fatalf("mutated state = %#v", got)
	}
}

func TestNilResponseAndSerializationErrors(t *testing.T) {
	var value *int
	result := commands.WithResponse(concepts.UUID{}, value)
	if response, present := result.Response(); !present || response != nil {
		t.Fatal("local nil presence lost")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"response"`) {
		t.Fatalf("typed nil emitted: %s", data)
	}
	if _, err = json.Marshal(commands.WithResponse(concepts.UUID{}, make(chan int))); err == nil {
		t.Fatal("silently serialized unsupported payload")
	}
	var zero commands.Result[int]
	if zero.IsAuthorized() || zero.IsSuccess() || zero.StatusCode() != 403 {
		t.Fatal("zero result must fail closed")
	}
}
