//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"os"
	"testing"
)

// runKnownFlakesEnv opts in to tests that are skipped because of a known
// upstream kernel defect. Set it to "1" to verify a kernel fix before removing
// the corresponding skip.
const runKnownFlakesEnv = "ARC_CHRONICLE_RUN_KNOWN_FLAKES"

// chronicle4548 names the upstream kernel defect where a finishing catch-up job
// is reused and strands an observer behind the event-log tail.
const chronicle4548 = "known upstream kernel defect https://github.com/Cratis/Chronicle/issues/4548"

// chronicle4583 names the upstream kernel defect where observer-wide catch-up
// progress moves past an event its partition never handled.
const chronicle4583 = "known upstream kernel defect https://github.com/Cratis/Chronicle/issues/4583"

// projectionStrandIssue tracks re-enabling the shared-model kernel contract
// once the Chronicle#4548 and Chronicle#4583 fixes ship.
const projectionStrandIssue = "https://github.com/Cratis/Arc.Go/issues/43"

// reactorStrandIssue tracks re-enabling the reactor kernel contract once the
// Chronicle#4548 fix ships.
const reactorStrandIssue = "https://github.com/Cratis/Arc.Go/issues/44"

func runKnownFlakes() bool {
	return os.Getenv(runKnownFlakesEnv) == "1"
}

// knownKernelDefectObserved ends a test whose failure matched the signature of
// a known upstream kernel defect. It skips by default and fails when
// ARC_CHRONICLE_RUN_KNOWN_FLAKES=1, so the evidence stays visible either way.
func knownKernelDefectObserved(t *testing.T, reason string, evidence ...any) {
	t.Helper()
	t.Log(evidence...)
	if runKnownFlakes() {
		t.Fatal(reason)
	}
	t.Skip(reason + " (set " + runKnownFlakesEnv + "=1 to fail instead)")
}
