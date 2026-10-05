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

// reactorStrandIssue tracks re-enabling the reactor kernel contract once the
// Chronicle#4548 fix ships.
const reactorStrandIssue = "https://github.com/Cratis/Arc.Go/issues/44"

func runKnownFlakes() bool {
	return os.Getenv(runKnownFlakesEnv) == "1"
}

// skipKnownKernelDefect skips a test that cannot pass reliably until an
// upstream kernel fix ships, unless ARC_CHRONICLE_RUN_KNOWN_FLAKES=1.
func skipKnownKernelDefect(t *testing.T, reason string) {
	t.Helper()
	if runKnownFlakes() {
		t.Logf("%s=1: running despite %s", runKnownFlakesEnv, reason)
		return
	}
	t.Skip(reason + " (set " + runKnownFlakesEnv + "=1 to run)")
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
