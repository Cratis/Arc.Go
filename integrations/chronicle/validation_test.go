// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cratis/arc.go/commands"
	chronicle "github.com/cratis/arc.go/integrations/chronicle"
)

func TestCommitMappingPreservesMixedFailuresAndWireMembers(t *testing.T) {
	cause := errors.New("future append code 713")
	result := chronicle.CommitResult{Report: commands.CompletionReport{Disposition: commands.NotCommitted}, Constraints: []chronicle.ConstraintViolation{{Name: "unique-name", Message: "already used", Property: "Name", Type: "future", Details: map[string]string{"secret": "never on wire"}}}, Concurrency: []chronicle.ConcurrencyViolation{{Source: "author", Expected: ^uint64(0) - 2, Actual: 0}}, Errors: []error{cause}}
	err := result.Failure(struct {
		Name string `json:"displayName"`
	}{}, nil)
	var commit *chronicle.CommitError
	if !errors.Is(err, cause) || !errors.As(err, &commit) || commit.Result.Constraints[0].Type != "future" {
		t.Fatal(err)
	}
	envelope := commands.FromError[commands.NoResponse]([16]byte{}, err)
	findings := envelope.Details().ValidationResults
	if len(findings) != 2 || !envelope.HasExceptions() || findings[0].Members[0] != "displayName" || *findings[0].ReasonDetail != "unique-name" || !strings.Contains(findings[1].Message, "expected no events") {
		t.Fatal(envelope.Details())
	}
}
func TestUnknownNeverBecomesConcurrencyViolation(t *testing.T) {
	result := chronicle.CommitResult{Report: commands.CompletionReport{Disposition: commands.OutcomeUnknown}}
	err := result.Failure(nil, nil)
	envelope := commands.FromError[commands.NoResponse]([16]byte{}, err)
	if !errors.Is(err, chronicle.ErrUnknownOutcome) || len(envelope.Details().ValidationResults) != 0 || !envelope.HasExceptions() {
		t.Fatal(envelope, err)
	}
}
func TestCommitDispositionIsIndependentOfOperationError(t *testing.T) {
	for _, disposition := range []commands.CommitDisposition{commands.NoPersistedWork, commands.NotCommitted, commands.Committed, commands.OutcomeUnknown, commands.MixedCommit} {
		result := chronicle.CommitResult{Report: commands.CompletionReport{Disposition: disposition}}
		err := result.Failure(nil, nil)
		success := disposition == commands.Committed || disposition == commands.NoPersistedWork
		if (err == nil) != success {
			t.Fatal(disposition, err)
		}
	}
}
