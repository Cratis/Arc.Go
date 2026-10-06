// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

type row struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type statusResult interface{ StatusCode() int }

func TestGoldenEnvelopes(t *testing.T) {
	id, err := concepts.ParseUUID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	finding := validation.Result{Severity: validation.Error, Message: "Name is required", Members: []string{"name"}}
	malformed := validation.Result{Severity: validation.Error, Message: "The request body could not be read or is not valid for this command.", Reason: validation.MalformedRequest}
	messages := []string{"An internal error occurred while processing the request. See server logs for details."}
	detail := "UniqueEmail"
	actual := map[string]any{
		"command-success":        commands.Success(id),
		"command-response":       commands.WithResponse(id, row{"a1", "Ada"}),
		"command-zero":           commands.WithResponse(id, 0),
		"command-false":          commands.WithResponse(id, false),
		"command-empty":          commands.WithResponse(id, ""),
		"command-denied":         commands.NewResult(commands.Details{CorrelationID: id, AuthorizationFailureReason: "Fixture filter denied"}, serialization.Optional[int]{}),
		"command-validation":     commands.NewResult(commands.Details{CorrelationID: id, Authorized: true, ValidationResults: []validation.Result{finding}}, serialization.Optional[int]{}),
		"command-malformed":      commands.NewResult(commands.Details{CorrelationID: id, Authorized: true, ValidationResults: []validation.Result{malformed}}, serialization.Optional[int]{}),
		"command-exception":      commands.NewResult(commands.Details{CorrelationID: id, Authorized: true, ExceptionMessages: messages}, serialization.Optional[int]{}),
		"query-success":          queries.Success(id, []row{}),
		"query-page":             queries.NewResult(queries.Details{CorrelationID: id, Authorized: true, Ready: true, Paging: queries.PagingInfo{Page: 1, Size: 25, TotalItems: 51}}, serialization.Some([]row{{"a1", "Ada"}})),
		"query-null":             queries.Success[*row](id, nil),
		"query-pending":          queries.NotReady[row](id),
		"query-denied":           queries.NewResult(queries.Details{CorrelationID: id, Ready: true}, serialization.Optional[row]{}),
		"query-validation":       queries.NewResult(queries.Details{CorrelationID: id, Authorized: true, Ready: true, ValidationResults: []validation.Result{finding}}, serialization.Optional[row]{}),
		"query-exception":        queries.NewResult(queries.Details{CorrelationID: id, Authorized: true, Ready: true, ExceptionMessages: messages}, serialization.Optional[row]{}),
		"query-delta":            queries.NewResult(queries.Details{CorrelationID: id, Authorized: true, Ready: true, ChangeSet: &queries.ChangeSet{Added: []any{row{"a1", "Ada"}}}}, serialization.Optional[[]row]{}),
		"validation-state":       validation.Result{Severity: validation.Error, Message: "The value is already used", Members: []string{"email"}, State: struct{ ApplicationCode string }{"duplicate"}, Reason: validation.ConstraintViolation, ReasonDetail: &detail},
		"validation-unknown":     validation.Result{Message: "Future category", Reason: "futureReason"},
		"validation-information": validation.Result{Severity: validation.Information, Message: "Information"},
		"validation-warning":     validation.Result{Severity: validation.Warning, Message: "Warning"},
	}
	data, err := os.ReadFile("fixtures/v1/envelopes.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]struct {
		Status int
		Body   json.RawMessage
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 21 || len(actual) != len(fixtures) {
		t.Fatalf("fixture coverage: %d fixtures, %d cases", len(fixtures), len(actual))
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			value, ok := actual[name]
			if !ok {
				t.Fatal("missing fixture implementation")
			}
			got, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(canonical(t, got), canonical(t, fixture.Body)) {
				t.Fatalf("JSON = %s\nwant = %s", got, fixture.Body)
			}
			if result, ok := value.(statusResult); ok && result.StatusCode() != fixture.Status {
				t.Errorf("status = %d, want %d", result.StatusCode(), fixture.Status)
			}
		})
	}
}

// Only whitespace and object key ordering are normalized. UseNumber preserves
// numeric spellings; null, omitted properties, scalar zeros and array order differ.
func canonical(t *testing.T, data []byte) []byte {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
