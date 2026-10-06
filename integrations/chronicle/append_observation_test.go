// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"fmt"
	"testing"

	"github.com/cratis/arc.go/commands"
)

func TestObservedCompletionMatchesArcMerge(t *testing.T) {
	// Rows and columns: no work, rejected, committed, unknown, mixed.
	want := [][]commands.CommitDisposition{
		{commands.NoPersistedWork, commands.NotCommitted, commands.Committed, commands.OutcomeUnknown, commands.MixedCommit},
		{commands.NotCommitted, commands.NotCommitted, commands.MixedCommit, commands.OutcomeUnknown, commands.MixedCommit},
		{commands.Committed, commands.MixedCommit, commands.Committed, commands.MixedCommit, commands.MixedCommit},
		{commands.OutcomeUnknown, commands.OutcomeUnknown, commands.MixedCommit, commands.OutcomeUnknown, commands.MixedCommit},
		{commands.MixedCommit, commands.MixedCommit, commands.MixedCommit, commands.MixedCommit, commands.MixedCommit},
	}
	for left, row := range want {
		for right, disposition := range row {
			t.Run(fmt.Sprintf("%d+%d", left, right), func(t *testing.T) {
				got := mergeObserved(commands.CompletionReport{Disposition: commands.CommitDisposition(left)}, commands.CompletionReport{Disposition: commands.CommitDisposition(right)})
				if got.Disposition != disposition {
					t.Fatalf("disposition = %v, want %v", got.Disposition, disposition)
				}
			})
		}
	}
}
