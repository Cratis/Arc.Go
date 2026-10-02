// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cratis/arc.go/validation"
)

func TestNilStateOmittedAndOpenReasonsRoundTrip(t *testing.T) {
	var state *int
	result := validation.Result{State: state, Reason: "futureReason"}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"state"`) || strings.Contains(string(data), `"reasonDetail"`) {
		t.Fatalf("nil properties emitted: %s", data)
	}
	var decoded validation.Result
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Reason != "futureReason" || decoded.Members == nil {
		t.Fatalf("round trip = %#v", decoded)
	}
	result.State = make(chan int)
	if _, err = json.Marshal(result); err == nil {
		t.Fatal("unsupported state silently serialized")
	}
}
