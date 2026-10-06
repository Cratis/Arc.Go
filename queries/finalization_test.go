// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"testing"

	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

func TestFailedFinalizationRetractsPagingDataAndChanges(t *testing.T) {
	for _, mode := range []string{"denied", "validation", "exception"} {
		t.Run(mode, func(t *testing.T) {
			details := Details{Ready: true, Authorized: true, Paging: PagingInfo{Page: 4, Size: 10, TotalItems: 51}, ChangeSet: &ChangeSet{Added: []any{"sensitive"}}}
			switch mode {
			case "denied":
				details.Authorized = false
			case "validation":
				details.ValidationResults = []validation.Result{{Severity: validation.Error}}
			case "exception":
				details.ExceptionMessages = []string{"private"}
			}
			result := finalize(NewResult(details, serialization.Some("sensitive")), false)
			if _, present := result.Data(); present || result.Details().ChangeSet != nil || result.Details().Paging != (PagingInfo{}) {
				t.Fatal("failed finalization retained publication metadata", result.Details())
			}
		})
	}
}
